package main

import (
	"fmt"
	"github.com/ochinchina/supervisord/config"
)

func main() {
	// Test loading the supervisor config
	configPath := "/Users/zph/.mup/storage/clusters/cluster-v3.6/supervisor.ini"
	cfg := config.NewConfig(configPath)

	changed, err := cfg.Load()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}

	fmt.Printf("Config loaded. Changed files: %d\n", len(changed))

	// Get all program names
	programs := cfg.GetProgramNames()
	fmt.Printf("Found %d programs:\n", len(programs))
	for _, name := range programs {
		fmt.Printf("  - %s\n", name)
	}

	// Get all programs using GetPrograms()
	allProgs := cfg.GetPrograms()
	fmt.Printf("\nGetPrograms() returned %d entries:\n", len(allProgs))
	for _, prog := range allProgs {
		fmt.Printf("  - %s (IsProgram: %v)\n", prog.Name, prog.IsProgram())
	}
}
