package upgrade

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	// versionVerifyTimeout bounds how long a restarted node may take to
	// accept connections before version verification fails.
	versionVerifyTimeout = 2 * time.Minute
	versionVerifyPoll    = time.Second
	versionLookupTimeout = 5 * time.Second
	localMongoHost       = "127.0.0.1"
)

// versionLookup returns the version a running server reports.
type versionLookup func(ctx context.Context) (string, error)

// portFromNodeID extracts the port from a supervisor program name such as
// "mongod-30000" or "config-27019".
func portFromNodeID(nodeID string) (int, error) {
	idx := strings.LastIndex(nodeID, "-")
	if idx < 0 {
		return 0, fmt.Errorf("node id %q has no port", nodeID)
	}
	port, err := strconv.Atoi(nodeID[idx+1:])
	if err != nil {
		return 0, fmt.Errorf("node id %q has an invalid port: %w", nodeID, err)
	}
	return port, nil
}

// waitForServerVersion polls lookup until the server answers, then checks the
// version it reports. A wrong version fails immediately; an unreachable server
// is retried until timeout.
// UPGSAFE-007
func waitForServerVersion(ctx context.Context, lookup versionLookup, expected string, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)

	for {
		actual, err := lookup(ctx)
		if err == nil {
			if !versionMatches(actual, expected) {
				return fmt.Errorf("node is running version %s, expected %s", actual, expected)
			}
			return nil
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("version verification interrupted: %w", ctxErr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("node did not report its version within %s: %w", timeout, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("version verification interrupted: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// driverVersionLookup asks the server on a local port for its buildInfo.
func driverVersionLookup(port int) versionLookup {
	return func(ctx context.Context) (string, error) {
		lookupCtx, cancel := context.WithTimeout(ctx, versionLookupTimeout)
		defer cancel()

		clientOpts := options.Client().
			ApplyURI(fmt.Sprintf("mongodb://%s:%d", localMongoHost, port)).
			SetDirect(true).
			SetConnectTimeout(versionLookupTimeout).
			SetServerSelectionTimeout(versionLookupTimeout)

		client, err := mongo.Connect(lookupCtx, clientOpts)
		if err != nil {
			return "", fmt.Errorf("failed to connect: %w", err)
		}
		defer func() {
			if err := client.Disconnect(context.Background()); err != nil {
				fmt.Printf("Warning: failed to close version check connection: %v\n", err)
			}
		}()

		var info bson.M
		if err := client.Database("admin").RunCommand(lookupCtx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&info); err != nil {
			return "", fmt.Errorf("buildInfo failed: %w", err)
		}
		version, ok := info["version"].(string)
		if !ok || version == "" {
			return "", fmt.Errorf("buildInfo returned no version")
		}
		return version, nil
	}
}
