package upgrade

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// balancerControl reads and changes the sharded cluster balancer.
type balancerControl interface {
	Enabled(ctx context.Context) (bool, error)
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
}

// balancerModeOff is the balancerStatus mode of a disabled balancer.
const balancerModeOff = "off"

// balancerGuard keeps the balancer stopped for the duration of a sharded
// cluster upgrade, as MongoDB's upgrade procedure requires, and restores it
// afterwards only if the upgrade was what stopped it.
// UPGSAFE-008
type balancerGuard struct {
	control balancerControl
	state   *UpgradeState
	save    func() error
	runHook func(ctx context.Context, hook HookType) error
}

// pause stops the balancer if it is enabled. The decision is recorded in the
// upgrade state so a resumed upgrade still knows to restore it.
func (g *balancerGuard) pause(ctx context.Context) error {
	if g.stoppedByUpgrade() {
		fmt.Println("  Balancer was stopped by an earlier run of this upgrade")
		return nil
	}

	enabled, err := g.control.Enabled(ctx)
	if err != nil {
		return fmt.Errorf("failed to read balancer state: %w", err)
	}
	if !enabled {
		fmt.Println("  Balancer is already disabled; leaving it as is")
		return nil
	}

	if err := g.runHook(ctx, HookBeforeBalancerStop); err != nil {
		return fmt.Errorf("%s hook failed: %w", HookBeforeBalancerStop, err)
	}

	fmt.Print("  Stopping balancer... ")
	if err := g.control.Stop(ctx); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("failed to stop balancer: %w", err)
	}
	fmt.Println("✓")

	g.setStoppedByUpgrade(true)
	return g.save()
}

// restore starts the balancer again if this upgrade stopped it.
func (g *balancerGuard) restore(ctx context.Context) error {
	if !g.stoppedByUpgrade() {
		return nil
	}

	fmt.Print("  Starting balancer... ")
	if err := g.control.Start(ctx); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("failed to start balancer: %w", err)
	}
	fmt.Println("✓")

	g.setStoppedByUpgrade(false)
	if err := g.save(); err != nil {
		return err
	}

	if err := g.runHook(ctx, HookAfterBalancerStart); err != nil {
		fmt.Printf("Warning: %s hook failed: %v\n", HookAfterBalancerStart, err)
	}
	return nil
}

func (g *balancerGuard) stoppedByUpgrade() bool {
	g.state.mu.RLock()
	defer g.state.mu.RUnlock()
	return g.state.BalancerStoppedByUpgrade
}

func (g *balancerGuard) setStoppedByUpgrade(stopped bool) {
	g.state.mu.Lock()
	defer g.state.mu.Unlock()
	g.state.BalancerStoppedByUpgrade = stopped
}

// mongosBalancerControl drives the balancer through a mongos connection. It
// connects per call because mongos itself is restarted during the upgrade.
type mongosBalancerControl struct {
	connect func(ctx context.Context) (*mongo.Client, error)
}

func (c mongosBalancerControl) run(ctx context.Context, command string) (bson.M, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := client.Disconnect(context.Background()); err != nil {
			fmt.Printf("Warning: failed to close balancer connection: %v\n", err)
		}
	}()

	var result bson.M
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: command, Value: 1}}).Decode(&result); err != nil {
		return nil, fmt.Errorf("%s failed: %w", command, err)
	}
	return result, nil
}

func (c mongosBalancerControl) Enabled(ctx context.Context) (bool, error) {
	status, err := c.run(ctx, "balancerStatus")
	if err != nil {
		return false, err
	}
	mode, ok := status["mode"].(string)
	if !ok {
		return false, fmt.Errorf("balancerStatus returned no mode")
	}
	return mode != balancerModeOff, nil
}

func (c mongosBalancerControl) Stop(ctx context.Context) error {
	_, err := c.run(ctx, "balancerStop")
	return err
}

func (c mongosBalancerControl) Start(ctx context.Context) error {
	_, err := c.run(ctx, "balancerStart")
	return err
}
