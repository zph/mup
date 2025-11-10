package topology

import (
	"fmt"
	"sort"
)

const (
	// DefaultBasePort is the starting port for local deployments
	DefaultBasePort = 30000

	// Port offsets for different component types
	MongodPortOffset    = 0
	MongosPortOffset    = 1000
	ConfigSvrPortOffset = 2000

	// MaxPortScanAttempts is the maximum number of ports to try before giving up
	MaxPortScanAttempts = 1000
)

// PortChecker is a function that checks if a port is available
type PortChecker func(port int) (bool, error)

// PortAllocator allocates sequential ports for local deployments
type PortAllocator struct {
	basePort    int
	nextPort    int
	allocations map[string]int // nodeID -> port
	checker     PortChecker    // optional port availability checker
}

// NewPortAllocator creates a new port allocator
func NewPortAllocator(basePort int) *PortAllocator {
	if basePort == 0 {
		basePort = DefaultBasePort
	}

	return &PortAllocator{
		basePort:    basePort,
		nextPort:    basePort,
		allocations: make(map[string]int),
		checker:     nil,
	}
}

// NewPortAllocatorWithChecker creates a port allocator with availability checking
func NewPortAllocatorWithChecker(basePort int, checker PortChecker) *PortAllocator {
	if basePort == 0 {
		basePort = DefaultBasePort
	}

	return &PortAllocator{
		basePort:    basePort,
		nextPort:    basePort,
		allocations: make(map[string]int),
		checker:     checker,
	}
}

// AllocateMongodPort allocates a port for a mongod node
func (pa *PortAllocator) AllocateMongodPort(host string, index int) (int, error) {
	nodeID := fmt.Sprintf("%s:mongod:%d", host, index)
	if port, exists := pa.allocations[nodeID]; exists {
		return port, nil
	}

	port := pa.basePort + MongodPortOffset + index

	// If we have a checker, find the next available port
	if pa.checker != nil {
		var err error
		port, err = pa.findAvailablePort(port)
		if err != nil {
			return 0, err
		}
	}

	pa.allocations[nodeID] = port
	pa.updateNextPort(port)
	return port, nil
}

// AllocateMongosPort allocates a port for a mongos node
func (pa *PortAllocator) AllocateMongosPort(host string, index int) (int, error) {
	nodeID := fmt.Sprintf("%s:mongos:%d", host, index)
	if port, exists := pa.allocations[nodeID]; exists {
		return port, nil
	}

	port := pa.basePort + MongosPortOffset + index

	// If we have a checker, find the next available port
	if pa.checker != nil {
		var err error
		port, err = pa.findAvailablePort(port)
		if err != nil {
			return 0, err
		}
	}

	pa.allocations[nodeID] = port
	pa.updateNextPort(port)
	return port, nil
}

// AllocateConfigSvrPort allocates a port for a config server node
func (pa *PortAllocator) AllocateConfigSvrPort(host string, index int) (int, error) {
	nodeID := fmt.Sprintf("%s:config:%d", host, index)
	if port, exists := pa.allocations[nodeID]; exists {
		return port, nil
	}

	port := pa.basePort + ConfigSvrPortOffset + index

	// If we have a checker, find the next available port
	if pa.checker != nil {
		var err error
		port, err = pa.findAvailablePort(port)
		if err != nil {
			return 0, err
		}
	}

	pa.allocations[nodeID] = port
	pa.updateNextPort(port)
	return port, nil
}

// findAvailablePort finds the next available port starting from the given port
func (pa *PortAllocator) findAvailablePort(startPort int) (int, error) {
	for attempt := 0; attempt < MaxPortScanAttempts; attempt++ {
		port := startPort + attempt

		// Check if already allocated
		alreadyAllocated := false
		for _, allocatedPort := range pa.allocations {
			if allocatedPort == port {
				alreadyAllocated = true
				break
			}
		}
		if alreadyAllocated {
			continue
		}

		// Check if available on the system
		available, err := pa.checker(port)
		if err != nil {
			return 0, fmt.Errorf("failed to check port %d: %w", port, err)
		}

		if available {
			return port, nil
		}
	}

	return 0, fmt.Errorf("failed to find available port after %d attempts starting from %d",
		MaxPortScanAttempts, startPort)
}

// updateNextPort updates the next available port
func (pa *PortAllocator) updateNextPort(port int) {
	if port >= pa.nextPort {
		pa.nextPort = port + 1
	}
}

// GetAllocatedPorts returns all allocated ports sorted
func (pa *PortAllocator) GetAllocatedPorts() []int {
	ports := make([]int, 0, len(pa.allocations))
	for _, port := range pa.allocations {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

// AllocatePortsForTopology allocates ports for all nodes in a local topology
func AllocatePortsForTopology(topo *Topology, checker PortChecker) error {
	if !topo.IsLocalDeployment() {
		return fmt.Errorf("port allocation is only for local deployments")
	}

	var allocator *PortAllocator
	if checker != nil {
		allocator = NewPortAllocatorWithChecker(DefaultBasePort, checker)
	} else {
		allocator = NewPortAllocator(DefaultBasePort)
	}

	// Allocate ports for mongod nodes
	for i := range topo.Mongod {
		node := &topo.Mongod[i]
		if node.Port == 0 {
			port, err := allocator.AllocateMongodPort(node.Host, i)
			if err != nil {
				return fmt.Errorf("failed to allocate port for mongod %s: %w", node.Host, err)
			}
			node.Port = port
		}
	}

	// Allocate ports for mongos nodes
	for i := range topo.Mongos {
		node := &topo.Mongos[i]
		if node.Port == 0 {
			port, err := allocator.AllocateMongosPort(node.Host, i)
			if err != nil {
				return fmt.Errorf("failed to allocate port for mongos %s: %w", node.Host, err)
			}
			node.Port = port
		}
	}

	// Allocate ports for config server nodes
	for i := range topo.ConfigSvr {
		node := &topo.ConfigSvr[i]
		if node.Port == 0 {
			port, err := allocator.AllocateConfigSvrPort(node.Host, i)
			if err != nil {
				return fmt.Errorf("failed to allocate port for config server %s: %w", node.Host, err)
			}
			node.Port = port
		}
	}

	return nil
}
