package client_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/nm1996/beacon/client"
)

// Discover the instances of a service and pick one to call.
func ExampleClient_Discover() {
	beacon, err := client.New("http://beacon:8080")
	if err != nil {
		log.Fatal(err)
	}

	instances, err := beacon.Discover(context.Background(), "orders")
	if err != nil {
		log.Fatal(err)
	}

	for _, instance := range instances {
		fmt.Printf("%s:%d\n", instance.Address, instance.Port)
	}
}

// Register by hand and react to Beacon having forgotten the instance.
func ExampleClient_Heartbeat() {
	ctx := context.Background()

	beacon, err := client.New("http://beacon:8080")
	if err != nil {
		log.Fatal(err)
	}

	registered, err := beacon.Register(ctx, client.Registration{Name: "orders", Address: "10.0.0.12", Port: 8081})
	if err != nil {
		log.Fatal(err)
	}

	if err := beacon.Heartbeat(ctx, registered.ID); errors.Is(err, client.ErrNotFound) {
		// Beacon restarted or the instance expired: register again.
		registered, err = beacon.Register(ctx, client.Registration{Name: "orders", Address: "10.0.0.12", Port: 8081})
		if err != nil {
			log.Fatal(err)
		}
	}

	fmt.Println(registered.ID)
}
