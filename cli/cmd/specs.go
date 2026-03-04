package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudquery/cloudquery/cli/v6/internal/specs/v0"
	"github.com/cloudquery/plugin-pb-go/pb/plugin/v3"
	pbSpecs "github.com/cloudquery/plugin-pb-go/specs"
	"github.com/rs/zerolog/log"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func CLIRegistryToPbRegistry(registry specs.Registry) (pbSpecs.Registry, error) {
	switch registry {
	case specs.RegistryGitHub:
		return pbSpecs.RegistryGithub, nil
	case specs.RegistryLocal:
		return pbSpecs.RegistryLocal, nil
	case specs.RegistryGRPC:
		return pbSpecs.RegistryGrpc, nil
	case specs.RegistryCloudQuery:
		return pbSpecs.RegistryCloudQuery, nil
	default:
		return 0, fmt.Errorf("unknown registry %q", registry.String())
	}
}

// This converts CLI configuration to a source spec prior to V3 version
// when our spec wasn't decoupled from the over the wire protocol
func CLISourceSpecToPbSpec(spec specs.Source) (pbSpecs.Source, error) {
	reg, err := CLIRegistryToPbRegistry(spec.Registry)
	if err != nil {
		return pbSpecs.Source{}, err
	}
	return pbSpecs.Source{
		Name:                spec.Name,
		Version:             spec.Version,
		Path:                spec.Path,
		Registry:            reg,
		Tables:              spec.Tables,
		SkipTables:          spec.SkipTables,
		SkipDependentTables: *spec.SkipDependentTables,
		Destinations:        spec.Destinations,
		Spec:                spec.Spec,
		DeterministicCQID:   spec.DeterministicCQID,
	}, nil
}

func CLIWriteModeToPbWriteMode(writeMode specs.WriteMode) (pbSpecs.WriteMode, error) {
	switch writeMode {
	case specs.WriteModeAppend:
		return pbSpecs.WriteModeAppend, nil
	case specs.WriteModeOverwrite:
		return pbSpecs.WriteModeOverwrite, nil
	case specs.WriteModeOverwriteDeleteStale:
		return pbSpecs.WriteModeOverwriteDeleteStale, nil
	default:
		return 0, fmt.Errorf("unknown write mode %q", writeMode.String())
	}
}

func CLIMigrateModeToPbMigrateMode(migrateMode specs.MigrateMode) (pbSpecs.MigrateMode, error) {
	switch migrateMode {
	case specs.MigrateModeSafe:
		return pbSpecs.MigrateModeSafe, nil
	case specs.MigrateModeForced:
		return pbSpecs.MigrateModeForced, nil
	default:
		return 0, fmt.Errorf("unknown migrate mode %q", migrateMode.String())
	}
}

func CLIPkModeToPbPKMode(pkMode specs.PKMode) (pbSpecs.PKMode, error) {
	switch pkMode {
	case specs.PKModeCQID:
		return pbSpecs.PKModeCQID, nil
	case specs.PKModeDefaultKeys:
		return pbSpecs.PKModeDefaultKeys, nil
	default:
		return 0, fmt.Errorf("unknown pk mode %q", pkMode.String())
	}
}

func CLIDestinationSpecToPbSpec(spec specs.Destination) (pbSpecs.Destination, error) {
	reg, err := CLIRegistryToPbRegistry(spec.Registry)
	if err != nil {
		return pbSpecs.Destination{}, err
	}
	wm, err := CLIWriteModeToPbWriteMode(spec.WriteMode)
	if err != nil {
		return pbSpecs.Destination{}, err
	}
	mm, err := CLIMigrateModeToPbMigrateMode(spec.MigrateMode)
	if err != nil {
		return pbSpecs.Destination{}, err
	}
	pk, err := CLIPkModeToPbPKMode(spec.PKMode)
	if err != nil {
		return pbSpecs.Destination{}, err
	}
	return pbSpecs.Destination{
		Name:        spec.Name,
		Version:     spec.Version,
		Path:        spec.Path,
		Registry:    reg,
		WriteMode:   wm,
		MigrateMode: mm,
		PKMode:      pk,
		Spec:        spec.Spec,
	}, nil
}

// initPlugin is a simple wrapper that will try to validate the spec before actually passing it to Init.
func initPlugin(ctx context.Context, client plugin.PluginClient, spec map[string]any, noConnection bool, syncID string) error {
	specBytes, err := marshalSpec(spec)
	if err != nil {
		return err
	}

	_, err = client.Init(ctx, &plugin.Init_Request{Spec: specBytes, NoConnection: noConnection, InvocationId: syncID})
	return err
}

// validatePluginSpec encompasses spec validation only:
//  1. Get spec schema from the plugin.
//     If the call isn't implemented, just skip the validation.
//  2. Validate that the provided JSON schema is valid & can be used for spec validation.
//     If the spec is empty (i.e., the plugin didn't supply the schema) just skip.
//  3. If the schema isn't empty but not valid, print the error message & skip the validation.
//  4. Finally, return the validation result.
func validatePluginSpec(ctx context.Context, client plugin.PluginClient, spec any) error {
	schema, err := client.GetSpecSchema(ctx, &plugin.GetSpecSchema_Request{})
	if err != nil {
		st, ok := status.FromError(err)
		if !ok {
			// not a gRPC-compatible error
			log.Err(err).Msg("failed to get spec schema")
			return err
		}
		if st.Code() != codes.Unimplemented {
			// unimplemented is OK, treat as empty schema
			log.Err(err).Msg("failed to get spec schema")
			return err
		}
	}

	jsonSchema := schema.GetJsonSchema()
	if len(jsonSchema) == 0 {
		// This will also be true for Unimplemented response (schema = nil => schema.GetJsonSchema() = "")
		log.Info().Msg("empty JSON schema for plugin spec, skipping validation")
		return nil
	}

	sc, err := parseJSONSchema(jsonSchema)
	if err != nil {
		log.Err(err).Msg("failed to parse spec schema, skipping validation")
		return nil
	}

	return sc.Validate(spec)
}

func parseJSONSchema(jsonSchema string) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()

	s, err := jsonschema.UnmarshalJSON(strings.NewReader(jsonSchema))
	if err != nil {
		return nil, err
	}

	if err := c.AddResource("schema.json", s); err != nil {
		return nil, err
	}

	sc, err := c.Compile("schema.json")
	if err != nil {
		var se *jsonschema.SchemaValidationError
		if errors.As(err, &se); se != nil && se.Err != nil {
			// We add resource as `file`, but there's none, actually.
			// So, we need to prettify message a bit.
			return nil, fmt.Errorf("jsonschema compilation failed: %w",
				errors.New(strings.Replace(se.Err.Error(), "jsonschema: '' ", "", 1)))
		}
		return nil, err
	}

	return sc, nil
}

func marshalSpec(spec map[string]any) ([]byte, error) {
	// All nil or empty values to be marshaled as null
	if len(spec) == 0 {
		return []byte(`null`), nil
	}

	return json.Marshal(spec)
}
