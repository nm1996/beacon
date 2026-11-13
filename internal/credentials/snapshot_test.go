package credentials

import (
	"crypto/x509"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nm1996/beacon/internal/tlstest"
)

// credentialSet is a client CA and the tokens file that maps that CA's
// client certificate to an identity. The two only make sense together.
type credentialSet struct {
	authority *tlstest.Authority
	tokens    string
	client    *x509.Certificate
}

// newCredentialSet returns a set whose client certificate has dnsName and
// maps to the identity name.
func newCredentialSet(test *testing.T, name, dnsName string) credentialSet {
	test.Helper()

	authority := tlstest.NewAuthority(test)
	client := authority.IssueClient(test, []string{dnsName})

	return credentialSet{
		authority: authority,
		tokens:    "[[certificates]]\nname = \"" + name + "\"\ndns = \"" + dnsName + "\"\n",
		client:    client.KeyPair.Leaf,
	}
}

// accepts reports whether current verifies set's client certificate during
// the handshake and maps it to an identity, both from current alone.
func (set credentialSet) accepts(current *snapshot) bool {
	_, err := set.client.Verify(x509.VerifyOptions{
		Roots:     current.handshake.ClientCAs,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		return false
	}

	_, ok := current.identities.AuthenticateCertificate(set.client)

	return ok
}

// TestEverySnapshotPairsMatchingCredentials reloads two matching sets of
// files back and forth while readers check every snapshot they see. It
// guards against a reload building a snapshot from mixed files and checks
// the generation count. That a snapshot is published in one step is
// guaranteed by Store having a single atomic.Pointer; a race window of a
// few nanoseconds is too small for a test like this to catch reliably.
func TestEverySnapshotPairsMatchingCredentials(test *testing.T) {
	serverAuthority := tlstest.NewAuthority(test)
	alpha := newCredentialSet(test, "alpha", "alpha.internal")
	beta := newCredentialSet(test, "beta", "beta.internal")

	store, settings := tlsStore(test, serverAuthority.IssueServer(test), alpha.authority, nil)
	writeFile(test, store.authSettings.TokensFile, []byte(alpha.tokens))

	if err := store.Reload(); err != nil {
		test.Fatalf("load alpha: %v", err)
	}

	var (
		stop       atomic.Bool
		readers    sync.WaitGroup
		mismatches atomic.Int64
		reads      atomic.Int64
	)

	// Readers take one snapshot at a time, as a handshake followed by a
	// request does, and check that both halves come from the same files:
	// the snapshot's CA must accept exactly the client its identities know.
	for range 4 {
		readers.Go(func() {
			for !stop.Load() {
				current := store.current.Load()
				if alpha.accepts(current) == beta.accepts(current) {
					mismatches.Add(1)
				}

				reads.Add(1)
			}
		})
	}

	// Each reload writes both files before reading them, so every reload
	// itself sees a matching pair.
	for round := range 200 {
		next := alpha
		if round%2 == 0 {
			next = beta
		}

		copyFile(test, next.authority.CertFile, settings.ClientCAFile)
		writeFile(test, store.authSettings.TokensFile, []byte(next.tokens))

		if err := store.Reload(); err != nil {
			test.Fatalf("reload %d: %v", round, err)
		}
	}

	stop.Store(true)
	readers.Wait()

	if got := mismatches.Load(); got != 0 {
		test.Errorf("%d of %d reads paired the TLS configuration of one reload with the identities of another", got, reads.Load())
	}

	if got := store.current.Load().generation; got != 201 {
		test.Errorf("expected generation 201 after the initial load and 201 reloads, got %d", got)
	}
}
