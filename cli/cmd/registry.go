package cmd

import (
	"fmt"

	"github.com/cloudquery/cloudquery/cli/v6/internal/specs/v0"
	"github.com/cloudquery/plugin-pb-go/managedplugin"
)

func SpecRegistryToPlugin(registry specs.Registry) (managedplugin.Registry, error) {
	switch registry {
	case specs.RegistryGitHub:
		return managedplugin.RegistryGithub, nil
	case specs.RegistryLocal:
		return managedplugin.RegistryLocal, nil
	case specs.RegistryGRPC:
		return managedplugin.RegistryGrpc, nil
	case specs.RegistryDocker:
		return managedplugin.RegistryDocker, nil
	case specs.RegistryCloudQuery:
		return managedplugin.RegistryCloudQuery, nil
	default:
		return 0, fmt.Errorf("unknown registry %q", registry.String())
	}
}
