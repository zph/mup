package operation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/zph/mup/pkg/executor"
)

const (
	// memberReadyTimeout bounds how long replica set initiation waits for
	// every member to accept connections. A mongod can take well over the
	// few seconds a fixed sleep allows, especially on a loaded machine.
	memberReadyTimeout = 3 * time.Minute
	memberReadyPoll    = time.Second
	memberProbeTimeout = 5 * time.Second
)

// memberProbe checks that one member accepts connections and answers a command.
type memberProbe func(ctx context.Context, host string) error

// waitForMembersReachable waits until every member answers the probe.
// replSetInitiate needs all proposed members to respond; initiating before
// they are up fails the whole deploy.
// LOCAL-039
func waitForMembersReachable(ctx context.Context, hosts []string, probe memberProbe, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	pending := append([]string(nil), hosts...)
	lastErrors := make(map[string]error)

	for {
		var stillPending []string
		for _, host := range pending {
			if err := probe(ctx, host); err != nil {
				stillPending = append(stillPending, host)
				lastErrors[host] = err
			}
		}
		pending = stillPending
		if len(pending) == 0 {
			return nil
		}

		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for replica set members: %w", err)
		}
		if time.Now().After(deadline) {
			details := make([]string, 0, len(pending))
			for _, host := range pending {
				details = append(details, fmt.Sprintf("%s (%v)", host, lastErrors[host]))
			}
			return fmt.Errorf("replica set members did not become reachable within %s: %s", timeout, strings.Join(details, ", "))
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for replica set members: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// pingMember returns a probe that pings a member over a direct connection.
func pingMember(exec executor.Executor) memberProbe {
	return func(ctx context.Context, host string) error {
		probeCtx, cancel := context.WithTimeout(ctx, memberProbeTimeout)
		defer cancel()

		client, err := NewMongoDBClient(probeCtx, host, exec)
		if err != nil {
			return err
		}
		defer func() {
			if err := client.Disconnect(context.Background()); err != nil {
				fmt.Printf("Warning: failed to close readiness connection to %s: %v\n", host, err)
			}
		}()

		_, err = client.RunCommand(probeCtx, bson.M{"ping": 1}, true)
		return err
	}
}
