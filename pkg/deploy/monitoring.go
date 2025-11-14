package deploy

import (
	"context"
	"fmt"

	"github.com/zph/mup/pkg/meta"
)

// monitoringMetadata stores monitoring metadata (will be saved to cluster meta)
var monitoringMetadata *meta.MonitoringMetadata

// deployMonitoring deploys monitoring infrastructure and exporters
func (d *Deployer) deployMonitoring(ctx context.Context) error {
	fmt.Println("\nPhase 4.5: Deploy Monitoring")
	fmt.Println("============================")

	// Step 1: Initialize monitoring infrastructure
	fmt.Println("Initializing monitoring infrastructure...")
	if err := d.monitoringMgr.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize monitoring: %w", err)
	}
	fmt.Println("  ✓ Victoria Metrics and Grafana images pulled")
	fmt.Println("  ✓ Supervisord configured for monitoring")
	fmt.Println("  ✓ Grafana provisioning configured")

	// Step 2: Start monitoring infrastructure
	fmt.Println("\nStarting monitoring infrastructure...")
	if err := d.monitoringMgr.StartClusterMonitoring(ctx, d.clusterName); err != nil {
		return fmt.Errorf("failed to start monitoring: %w", err)
	}
	fmt.Println("  ✓ Victoria Metrics started")
	fmt.Println("  ✓ Grafana started")

	// Step 3: Deploy exporters
	fmt.Println("\nDeploying metric exporters...")
	monitoringMeta, err := d.monitoringMgr.DeployExporters(ctx, d.clusterName, d.topology)
	if err != nil {
		return fmt.Errorf("failed to deploy exporters: %w", err)
	}

	// Store monitoring metadata for saving later
	monitoringMetadata = monitoringMeta

	fmt.Printf("  ✓ %d node_exporter(s) deployed\n", len(monitoringMeta.NodeExporters))
	fmt.Printf("  ✓ %d mongodb_exporter(s) deployed\n", len(monitoringMeta.MongoDBExporters))
	fmt.Println("  ✓ Scrape configuration generated")

	// Step 4: Display monitoring URLs
	fmt.Println("\nMonitoring URLs:")
	fmt.Println("----------------------------------------")

	urls, err := d.monitoringMgr.GetURLs()
	if err != nil {
		fmt.Printf("  Warning: Could not get monitoring URLs: %v\n", err)
	} else {
		fmt.Printf("  Grafana:          %s\n", urls.Grafana)

		password, err := d.monitoringMgr.GetGrafanaAdminPassword()
		if err == nil {
			fmt.Printf("  Grafana User:     admin\n")
			fmt.Printf("  Grafana Password: %s\n", password)
		}

		fmt.Printf("  Victoria Metrics: %s\n", urls.VictoriaMetrics)
		fmt.Printf("\n  Dashboards:\n")
		for _, dashboard := range urls.Dashboards {
			fmt.Printf("    - %s\n", dashboard.Name)
		}
	}

	fmt.Println("\n✓ Phase 4.5 complete: Monitoring deployed")
	return nil
}

// GetMonitoringMetadata returns the monitoring metadata for saving
func (d *Deployer) GetMonitoringMetadata() *meta.MonitoringMetadata {
	return monitoringMetadata
}
