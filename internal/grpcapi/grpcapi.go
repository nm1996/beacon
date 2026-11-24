// Package grpcapi serves the registry over gRPC, as defined in
// api/beacon/v1/registry.proto. It follows the HTTP API: every rule lives
// in the discovery layer, and authentication, permissions, rate limits and
// error reasons are the same, the limits being the very same instances so
// a client cannot double its budget by switching transports.
package grpcapi

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	beaconv1 "github.com/nm1996/beacon/api/beacon/v1"
	"github.com/nm1996/beacon/internal/auth"
	"github.com/nm1996/beacon/internal/discovery"
	"github.com/nm1996/beacon/internal/ratelimit"
)

// errorDomain is the domain of every google.rpc.ErrorInfo Beacon sends.
const errorDomain = "beacon"

// Paging limits, the same as the HTTP API's.
const (
	defaultPageSize = 500
	maxPageSize     = 1000
)

// Limits, as passed to Config.RateLimited and used by the HTTP API.
const (
	limitRegistrations = "registrations"
	limitAuthFailures  = "auth_failures"
)

// Discovery is what the server needs from the discovery layer.
// *discovery.Service satisfies it.
type Discovery interface {
	Register(ctx context.Context, registration discovery.Registration, caller discovery.Caller) (discovery.Instance, bool, error)
	Heartbeat(ctx context.Context, id string, caller discovery.Caller) error
	List(ctx context.Context, query discovery.ListQuery) ([]discovery.Instance, error)
	Unregister(ctx context.Context, id string, caller discovery.Caller) error
	TTL() time.Duration
}

// Authenticator finds the identity of a bearer token or a verified client
// certificate. *auth.Identities, and the credentials store, satisfy it.
type Authenticator interface {
	Authenticate(token string) (auth.Identity, bool)
	AuthenticateCertificate(certificate *x509.Certificate) (auth.Identity, bool)
}

// Config holds what the server shares with the HTTP API.
type Config struct {
	// Authenticator, if not nil, turns authentication on.
	Authenticator Authenticator
	// RegistrationLimit and FailureLimit are the HTTP API's limiters; nil
	// turns a limit off.
	RegistrationLimit *ratelimit.Limiter
	FailureLimit      *ratelimit.Limiter
	// TLS, if not nil, serves over TLS with it, asking for client
	// certificates as it says.
	TLS credentials.TransportCredentials
	// Observe, if not nil, is called after every call with its full method
	// name, status code and duration.
	Observe func(method string, code codes.Code, duration time.Duration)
	// RateLimited, if not nil, is called with the limit that refused a call.
	RateLimited func(limit string)
	// AuthFailed, if not nil, is called with the reason a call was refused:
	// missing, invalid or forbidden.
	AuthFailed func(reason string)
}

// permissions maps each RegistryService method to the permission it needs.
var permissions = map[string]auth.Permission{
	beaconv1.RegistryService_Register_FullMethodName:      auth.PermissionRegister,
	beaconv1.RegistryService_Heartbeat_FullMethodName:     auth.PermissionRegister,
	beaconv1.RegistryService_Unregister_FullMethodName:    auth.PermissionRegister,
	beaconv1.RegistryService_ListInstances_FullMethodName: auth.PermissionDiscover,
}

// NewServer returns a gRPC server with the registry service, the standard
// health service, reporting SERVING, and server reflection, so grpcurl
// works. Health and reflection need no credentials, like GET /health.
func NewServer(service Discovery, logger *slog.Logger, config Config) *grpc.Server {
	server := &registryServer{discovery: service, logger: logger, config: config}

	options := []grpc.ServerOption{grpc.ChainUnaryInterceptor(server.observe, server.authorize)}
	if config.TLS != nil {
		options = append(options, grpc.Creds(config.TLS))
	}

	grpcServer := grpc.NewServer(options...)
	beaconv1.RegisterRegistryServiceServer(grpcServer, server)

	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(beaconv1.RegistryService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	reflection.Register(grpcServer)

	return grpcServer
}

// registryServer implements beaconv1.RegistryServiceServer.
type registryServer struct {
	beaconv1.UnimplementedRegistryServiceServer

	discovery Discovery
	logger    *slog.Logger
	config    Config
}

// callerKey is the context key under which authorize stores the caller.
type callerKey struct{}

// authorized is who authorize let through, and the key their
// registrations are limited by.
type authorized struct {
	caller discovery.Caller
	key    string
}

// observe reports every call to Config.Observe.
func (server *registryServer) observe(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	started := time.Now()
	response, err := handler(ctx, request)

	if server.config.Observe != nil {
		server.config.Observe(info.FullMethod, status.Code(err), time.Since(started))
	}

	return response, err
}

// authorize checks the credentials of calls to RegistryService, as the
// HTTP Guard does, and stores the caller in the context. Without an
// Authenticator every caller may change every instance.
func (server *registryServer) authorize(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	permission, protected := permissions[info.FullMethod]
	if !protected {
		return handler(ctx, request)
	}

	network := peerNetwork(ctx)

	if server.config.Authenticator == nil {
		return handler(context.WithValue(ctx, callerKey{}, authorized{
			caller: discovery.Caller{AnyInstance: true},
			key:    "network:" + network,
		}), request)
	}

	reservation, ok, wait := server.config.FailureLimit.Reserve(network)
	if !ok {
		server.rateLimited(limitAuthFailures)
		return nil, rateLimitedError(wait, "Too many failed authentication attempts from this network")
	}

	identity, reason := server.identify(ctx)
	if reason == "invalid" {
		reservation.Spend()
	} else {
		reservation.Release()
	}

	if reason != "" {
		server.authFailed(reason)
		return nil, errorWithReason(codes.Unauthenticated, "unauthorized", "A valid bearer token or client certificate is required")
	}

	if !identity.Allows(permission) {
		server.authFailed("forbidden")
		return nil, errorWithReason(codes.PermissionDenied, "forbidden", fmt.Sprintf("%q does not have the %s permission", identity.Name, permission))
	}

	return handler(context.WithValue(ctx, callerKey{}, authorized{
		caller: discovery.Caller{ID: identity.ID, Name: identity.Name, AnyInstance: identity.Allows(auth.PermissionManage)},
		key:    "caller:" + identity.ID,
	}), request)
}

// identify finds the caller from the authorization metadata, or else the
// verified client certificate. The reason is "missing" or "invalid" when
// it cannot, and empty when it can.
func (server *registryServer) identify(ctx context.Context) (auth.Identity, string) {
	if values := metadata.ValueFromIncomingContext(ctx, "authorization"); len(values) > 0 {
		token, ok := strings.CutPrefix(values[0], "Bearer ")
		if !ok || len(values) > 1 {
			return auth.Identity{}, "invalid"
		}

		identity, ok := server.config.Authenticator.Authenticate(strings.TrimSpace(token))
		if !ok {
			return auth.Identity{}, "invalid"
		}

		return identity, ""
	}

	caller, ok := peer.FromContext(ctx)
	if !ok {
		return auth.Identity{}, "missing"
	}

	info, ok := caller.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 || len(info.State.VerifiedChains[0]) == 0 {
		return auth.Identity{}, "missing"
	}

	identity, ok := server.config.Authenticator.AuthenticateCertificate(info.State.VerifiedChains[0][0])
	if !ok {
		return auth.Identity{}, "invalid"
	}

	return identity, ""
}

// rateLimited reports a refusal by limit.
func (server *registryServer) rateLimited(limit string) {
	if server.config.RateLimited != nil {
		server.config.RateLimited(limit)
	}
}

// authFailed reports a refusal by authentication.
func (server *registryServer) authFailed(reason string) {
	if server.config.AuthFailed != nil {
		server.config.AuthFailed(reason)
	}
}

// callerFrom returns what authorize stored in ctx.
func callerFrom(ctx context.Context) authorized {
	caller, _ := ctx.Value(callerKey{}).(authorized)
	return caller
}

// Register adds or refreshes an instance, within the registration limit.
func (server *registryServer) Register(ctx context.Context, request *beaconv1.RegisterRequest) (*beaconv1.RegisterResponse, error) {
	caller := callerFrom(ctx)

	if ok, wait := server.config.RegistrationLimit.Allow(caller.key); !ok {
		server.rateLimited(limitRegistrations)
		return nil, rateLimitedError(wait, "Too many registrations from this caller")
	}

	instance, created, err := server.discovery.Register(ctx, discovery.Registration{
		ID:             request.GetId(),
		IdempotencyKey: request.GetIdempotencyKey(),
		Name:           request.GetName(),
		Address:        request.GetAddress(),
		Port:           int(request.GetPort()),
	}, caller.caller)
	if err != nil {
		return nil, server.discoveryError(ctx, err)
	}

	return &beaconv1.RegisterResponse{
		Id:      instance.ID,
		Ttl:     durationpb.New(server.discovery.TTL()),
		Created: created,
	}, nil
}

// Heartbeat renews an instance.
func (server *registryServer) Heartbeat(ctx context.Context, request *beaconv1.HeartbeatRequest) (*beaconv1.HeartbeatResponse, error) {
	if err := server.discovery.Heartbeat(ctx, request.GetId(), callerFrom(ctx).caller); err != nil {
		return nil, server.discoveryError(ctx, err)
	}

	return &beaconv1.HeartbeatResponse{}, nil
}

// Unregister removes an instance.
func (server *registryServer) Unregister(ctx context.Context, request *beaconv1.UnregisterRequest) (*beaconv1.UnregisterResponse, error) {
	if err := server.discovery.Unregister(ctx, request.GetId(), callerFrom(ctx).caller); err != nil {
		return nil, server.discoveryError(ctx, err)
	}

	return &beaconv1.UnregisterResponse{}, nil
}

// ListInstances returns a page of live instances.
func (server *registryServer) ListInstances(ctx context.Context, request *beaconv1.ListInstancesRequest) (*beaconv1.ListInstancesResponse, error) {
	size := int(request.GetPageSize())

	switch {
	case size == 0:
		size = defaultPageSize
	case size < 0 || size > maxPageSize:
		return nil, errorWithReason(codes.InvalidArgument, "invalid_query", fmt.Sprintf("page_size must be between 0 and %d", maxPageSize))
	}

	after := ""
	if token := request.GetPageToken(); token != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(decoded) == 0 {
			return nil, errorWithReason(codes.InvalidArgument, "invalid_query", "page_token was not issued by Beacon")
		}

		after = string(decoded)
	}

	// One more than the page holds, to learn whether another follows.
	instances, err := server.discovery.List(ctx, discovery.ListQuery{Name: request.GetName(), After: after, Limit: size + 1})
	if err != nil {
		return nil, server.discoveryError(ctx, err)
	}

	response := &beaconv1.ListInstancesResponse{}

	if len(instances) > size {
		instances = instances[:size]
		response.NextPageToken = base64.RawURLEncoding.EncodeToString([]byte(instances[len(instances)-1].ID))
	}

	for _, instance := range instances {
		response.Instances = append(response.Instances, &beaconv1.Instance{
			Id:            instance.ID,
			Name:          instance.Name,
			Address:       instance.Address,
			Port:          int32(instance.Port), //nolint:gosec // ports are validated to 1-65535
			LastHeartbeat: timestamppb.New(instance.LastHeartbeat),
		})
	}

	return response, nil
}

// discoveryError maps an error from the discovery layer to a status with
// the HTTP API's error code as its ErrorInfo reason. Unexpected errors are
// logged and reported without details.
func (server *registryServer) discoveryError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, discovery.ErrInvalidRegistration):
		return errorWithReason(codes.InvalidArgument, "invalid_service", err.Error())
	case errors.Is(err, discovery.ErrIdempotencyKeyReused):
		return errorWithReason(codes.InvalidArgument, "idempotency_key_reused", "This idempotency key was used for a registration with a different name, address or port")
	case errors.Is(err, discovery.ErrInstanceNotFound):
		return errorWithReason(codes.NotFound, "service_not_found", "No service is registered with this ID")
	case errors.Is(err, discovery.ErrInstanceNotOwned):
		return errorWithReason(codes.PermissionDenied, "instance_not_owned", "This instance was registered by another caller")
	case errors.Is(err, discovery.ErrInstanceConflict):
		return errorWithReason(codes.AlreadyExists, "instance_exists", "Another instance has this ID")
	case errors.Is(err, discovery.ErrRegistryFull), errors.Is(err, discovery.ErrOwnerQuotaExceeded):
		return errorWithReason(codes.ResourceExhausted, "quota_exceeded", "Beacon, or this caller, holds the maximum number of instances")
	default:
		server.logger.ErrorContext(ctx, "discovery operation failed", slog.Any("error", err))
		return errorWithReason(codes.Internal, "internal_error", "Internal server error")
	}
}

// errorWithReason returns a status with code and message, carrying an
// ErrorInfo with reason.
func errorWithReason(code codes.Code, reason, message string) error {
	result, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: errorDomain})
	if err != nil {
		return status.Error(code, message)
	}

	return result.Err()
}

// rateLimitedError is ResourceExhausted with reason rate_limited and a
// RetryInfo saying how long to wait, rounded up to whole seconds as the
// HTTP API's Retry-After is.
func rateLimitedError(wait time.Duration, message string) error {
	seconds := int64(math.MaxInt32)
	if wait < time.Duration(math.MaxInt64)-time.Second {
		seconds = max(1, int64((wait+time.Second-1)/time.Second))
	}

	result, err := status.New(codes.ResourceExhausted, message+"; retry after the delay in RetryInfo").WithDetails(
		&errdetails.ErrorInfo{Reason: "rate_limited", Domain: errorDomain, Metadata: map[string]string{"retry_after_seconds": strconv.FormatInt(seconds, 10)}},
		&errdetails.RetryInfo{RetryDelay: durationpb.New(time.Duration(seconds) * time.Second)},
	)
	if err != nil {
		return status.Error(codes.ResourceExhausted, message)
	}

	return result.Err()
}

// peerNetwork returns the network of the connection's peer, keyed as the
// HTTP API keys it: the IPv4 address or the IPv6 /64. gRPC calls carry no
// forwarded address, so trusted proxies do not apply.
func peerNetwork(ctx context.Context) string {
	caller, ok := peer.FromContext(ctx)
	if !ok || caller.Addr == nil {
		return "unknown"
	}

	host, _, err := net.SplitHostPort(caller.Addr.String())
	if err != nil {
		host = caller.Addr.String()
	}

	address, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}

	address = address.Unmap().WithZone("")
	if address.Is4() {
		return address.String()
	}

	prefix, err := address.Prefix(64)
	if err != nil {
		return address.String()
	}

	return prefix.String()
}
