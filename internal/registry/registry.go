package registry

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
)

const defaultMaximumBytes = 64 << 10

var (
	namePattern          = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	fieldPattern         = regexp.MustCompile(`^[a-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)*$`)
	responseFieldPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$`)
)

type Registry struct {
	version  string
	commands []CommandSpec
	byID     map[string]int
	byPath   map[string]int
}

func New(version string, commands ...CommandSpec) (*Registry, error) {
	r := &Registry{version: version, byID: make(map[string]int), byPath: make(map[string]int)}
	r.commands = make([]CommandSpec, len(commands))
	for i, command := range commands {
		command = cloneCommand(command)
		applyDefaults(&command)
		if err := validateCommand(command); err != nil {
			return nil, err
		}
		pathKey := strings.Join(command.Path, "\x00")
		if _, exists := r.byID[command.ID]; exists {
			return nil, fmt.Errorf("duplicate command ID %q", command.ID)
		}
		if _, exists := r.byPath[pathKey]; exists {
			return nil, fmt.Errorf("duplicate command path %q", strings.Join(command.Path, " "))
		}
		r.commands[i] = command
		r.byID[command.ID] = i
		r.byPath[pathKey] = i
	}
	sort.Slice(r.commands, func(i, j int) bool { return r.commands[i].ID < r.commands[j].ID })
	r.reindex()
	return r, nil
}

func MustNew(version string, commands ...CommandSpec) *Registry {
	r, err := New(version, commands...)
	if err != nil {
		panic(err)
	}
	return r
}

func (r *Registry) Version() string { return r.version }

func (r *Registry) Commands() []CommandSpec {
	result := make([]CommandSpec, len(r.commands))
	for i := range r.commands {
		result[i] = cloneCommand(r.commands[i])
	}
	return result
}

func (r *Registry) Lookup(path ...string) (CommandSpec, bool) {
	index, ok := r.byPath[strings.Join(normalizePath(path), "\x00")]
	if !ok {
		return CommandSpec{}, false
	}
	return cloneCommand(r.commands[index]), true
}

func (r *Registry) LookupID(id string) (CommandSpec, bool) {
	index, ok := r.byID[id]
	if !ok {
		return CommandSpec{}, false
	}
	return cloneCommand(r.commands[index]), true
}

func (r *Registry) Index(prefix ...string) IndexDocument {
	prefix = normalizePath(prefix)
	entries := make([]IndexEntry, 0)
	for _, command := range r.commands {
		if hasPrefix(command.Path, prefix) {
			entries = append(entries, IndexEntry{
				ID: command.ID, Path: append([]string(nil), command.Path...), Summary: command.Summary,
				AgentInvocable: command.AgentInvocable, Auth: command.Auth, Effects: command.Effects,
			})
		}
	}
	return IndexDocument{
		SchemaVersion: CommandIndexSchemaVersion, CLIVersion: r.version,
		Prefix: append([]string(nil), prefix...), InvocationControls: invocationControls(), Commands: entries,
	}
}

func (r *Registry) Schema(path ...string) (SchemaDocument, bool) {
	command, ok := r.Lookup(path...)
	if !ok && len(path) == 1 && strings.Contains(path[0], ".") {
		command, ok = r.Lookup(strings.Split(path[0], ".")...)
	}
	if !ok {
		return SchemaDocument{}, false
	}
	return SchemaDocument{
		SchemaVersion: CommandSchemaVersion, CLIVersion: r.version,
		ID: command.ID, Path: command.Path, Summary: command.Summary, AgentInvocable: command.AgentInvocable, Params: command.Params,
		Response: command.Response, Auth: command.Auth, Effects: command.Effects,
		Output: command.Output, Pagination: command.Pagination, InvocationControls: invocationControls(), Examples: command.Examples,
		ErrorCodes: []string{
			"authentication_failed", "conflicting_inputs", "credential_parameter", "duplicate_flag",
			"duplicate_parameter", "execute_not_supported", "human_authorization_required", "internal_error", "interrupted", "invalid_fields", "invalid_flag", "invalid_flag_value",
			"invalid_decimal", "invalid_order_exposure", "invalid_output_format", "invalid_private_key",
			"invalid_parameter", "invalid_parameter_type", "invalid_parameter_value", "invalid_params",
			"invalid_provider_response", "invalid_resource_id", "invalid_schema_path", "invalid_signed_transaction", "invalid_timeout",
			"invalid_upstream_request", "invalid_upstream_response", "keychain_unavailable", "missing_command",
			"missing_flag_value", "missing_parameter", "not_found", "output_too_large", "parameter_too_large", "provider_rejected",
			"mutation_indeterminate", "order_notional_exceeded", "policy_denied", "provider_response_too_large", "provider_unavailable", "rate_limited", "sanitized_key_collision",
			"secret_input_unavailable", "timeout", "too_many_arguments", "unknown_command", "unknown_field", "unknown_flag", "unknown_parameter",
			"unsafe_input", "unsafe_wallet_metadata", "unsupported_flag", "unsupported_wallet_type", "upstream_not_configured",
			"upstream_output_too_large", "upstream_rejected", "upstream_timeout", "wallet_address_mismatch", "wallet_changed",
			"wallet_exists", "wallet_not_configured", "wallet_secret_unavailable", "wrong_chain",
		},
	}, true
}

func invocationControls() []InvocationControl {
	return []InvocationControl{
		{
			Name: "--compact", Type: KindBoolean, Default: false,
			Description: "Emit single-line JSON without indentation",
		},
		{
			Name: "--execute", Type: KindBoolean, Default: false,
			Description: "Execute a mutation after controlling-terminal operator confirmation; mutations dry-run by default",
		},
		{
			Name: "--fields", Type: KindString, MaximumBytes: 1024,
			Description: "Project response fields under data while retaining safety metadata",
		},
		{
			Name: "--json", Type: KindBoolean, Default: true,
			Description: "Emit the versioned machine-readable JSON envelope",
		},
		{
			Name: "--params", Type: KindString, Format: "json-object-or-stdin", MaximumBytes: defaultMaximumBytes,
			ConflictsWith: []string{"positionals", "convenience-request-flags"},
			Description:   "Invoke the command from one strict JSON object, or read it from stdin with '-'",
		},
		{
			Name: "--timeout", Type: KindString, Format: "duration", Default: "15s", Maximum: "1m",
			Description: "Bound the complete command execution time",
		},
	}
}

func (r *Registry) reindex() {
	r.byID = make(map[string]int, len(r.commands))
	r.byPath = make(map[string]int, len(r.commands))
	for i, command := range r.commands {
		r.byID[command.ID] = i
		r.byPath[strings.Join(command.Path, "\x00")] = i
	}
}

func validateCommand(command CommandSpec) error {
	if command.ID == "" || !validID(command.ID) {
		return fmt.Errorf("invalid command ID %q", command.ID)
	}
	if len(command.Path) == 0 {
		return fmt.Errorf("command %q has an empty path", command.ID)
	}
	if command.ID != strings.Join(command.Path, ".") {
		return fmt.Errorf("command ID %q must equal dotted path %q", command.ID, strings.Join(command.Path, "."))
	}
	for _, segment := range command.Path {
		if !namePattern.MatchString(segment) {
			return fmt.Errorf("command %q has invalid path segment %q", command.ID, segment)
		}
	}
	if strings.TrimSpace(command.Summary) == "" {
		return fmt.Errorf("command %q has no summary", command.ID)
	}
	if command.Params.MaximumBytes < 1 || command.Params.MaximumBytes > defaultMaximumBytes {
		return fmt.Errorf("command %q has invalid params maximum", command.ID)
	}
	seen := make(map[string]struct{}, len(command.Params.Fields))
	seenFlags := make(map[string]struct{}, len(command.Params.Fields))
	for _, field := range command.Params.Fields {
		if !fieldPattern.MatchString(field.Name) {
			return fmt.Errorf("command %q has invalid field name %q", command.ID, field.Name)
		}
		if _, exists := seen[field.Name]; exists {
			return fmt.Errorf("command %q has duplicate field %q", command.ID, field.Name)
		}
		seen[field.Name] = struct{}{}
		if field.Flag != "" {
			if !namePattern.MatchString(field.Flag) {
				return fmt.Errorf("command %q field %q has invalid flag %q", command.ID, field.Name, field.Flag)
			}
			if _, exists := seenFlags[field.Flag]; exists {
				return fmt.Errorf("command %q has duplicate flag %q", command.ID, field.Flag)
			}
			seenFlags[field.Flag] = struct{}{}
		}
		if field.Kind == "" {
			return fmt.Errorf("command %q field %q has no type", command.ID, field.Name)
		}
		if err := validateField(command.ID, field.Name, field); err != nil {
			return err
		}
	}
	if command.Response.Kind == "" {
		return fmt.Errorf("command %q response has no type", command.ID)
	}
	if err := validateValueSpec(command.ID, "response", command.Response); err != nil {
		return err
	}
	if command.Effects.Effects.Executed {
		return fmt.Errorf("command %q declares runtime execution in its static effects", command.ID)
	}
	if command.Output.MaximumProviderBytes < 1 || command.Output.MaximumEncodedOutputBytes < 1 {
		return fmt.Errorf("command %q has invalid output byte limits", command.ID)
	}
	if command.Output.DefaultItemLimit < 0 || command.Output.HardItemLimit < 0 ||
		command.Output.HardItemLimit > 0 && command.Output.DefaultItemLimit > command.Output.HardItemLimit {
		return fmt.Errorf("command %q has invalid item limits", command.ID)
	}
	if command.Pagination != nil && !command.Output.Collection {
		return fmt.Errorf("command %q declares pagination for a non-collection response", command.ID)
	}
	for _, format := range command.Output.Formats {
		if format != "human" && format != "json" && format != "ndjson" {
			return fmt.Errorf("command %q has unsupported output format %q", command.ID, format)
		}
		if format == "ndjson" && !command.Output.Collection {
			return fmt.Errorf("command %q enables NDJSON for a non-collection response", command.ID)
		}
	}
	seenResponseFields := make(map[string]struct{}, len(command.Output.ResponseFields))
	for _, path := range command.Output.ResponseFields {
		if !responseFieldPattern.MatchString(path) {
			return fmt.Errorf("command %q has invalid response field path %q", command.ID, path)
		}
		if _, exists := seenResponseFields[path]; exists {
			return fmt.Errorf("command %q has duplicate response field path %q", command.ID, path)
		}
		seenResponseFields[path] = struct{}{}
	}
	if command.Pagination != nil {
		pagination := command.Pagination
		if pagination.DefaultMaxItems < 1 || pagination.HardMaxItems < pagination.DefaultMaxItems ||
			pagination.DefaultMaxPages < 1 || pagination.HardMaxPages < pagination.DefaultMaxPages {
			return fmt.Errorf("command %q has invalid pagination bounds", command.ID)
		}
	}
	if command.Effects.Effects.IsMutation() {
		if !command.Effects.DryRun {
			return fmt.Errorf("mutation command %q must support dry-run", command.ID)
		}
		if command.Effects.Confirmation != ConfirmationPlanHash && command.Effects.Confirmation != ConfirmationTTY {
			return fmt.Errorf("mutation command %q must require plan-hash or controlling-terminal confirmation", command.ID)
		}
	}
	return nil
}

func applyDefaults(command *CommandSpec) {
	if command.Params.MaximumBytes == 0 {
		command.Params.MaximumBytes = defaultMaximumBytes
	}
	if command.Params.ConflictRule == "" {
		command.Params.ConflictRule = "cannot be combined with positional arguments or convenience request flags"
	}
	if len(command.Params.OutputControls) == 0 {
		command.Params.OutputControls = []string{"--json", "--compact", "--fields"}
		if command.Output.Collection {
			command.Params.OutputControls = append(command.Params.OutputControls, "--ndjson")
		}
	}
	if command.Output.MaximumProviderBytes == 0 {
		command.Output.MaximumProviderBytes = 8 << 20
	}
	if command.Output.MaximumEncodedOutputBytes == 0 {
		command.Output.MaximumEncodedOutputBytes = 8 << 20
	}
	if len(command.Output.Formats) == 0 {
		command.Output.Formats = []string{"human", "json"}
	}
	if command.Auth.Mode == "" {
		command.Auth.Mode = AuthNone
	}
	if command.Effects.Effects.Network == "" {
		command.Effects.Effects.Network = "none"
	}
	if command.Effects.Effects.Mutation == "" {
		command.Effects.Effects.Mutation = "none"
	}
	if command.Effects.Effects.Risk == "" {
		command.Effects.Effects.Risk = "none"
	}
	if command.Effects.Confirmation == "" {
		command.Effects.Confirmation = ConfirmationNone
	}
}

func validateField(commandID, path string, field FieldSpec) error {
	switch field.Kind {
	case KindString, KindBoolean, KindInteger, KindNumber, KindObject, KindArray:
	default:
		return fmt.Errorf("command %q field %q has invalid type %q", commandID, path, field.Kind)
	}
	if field.MaxBytes < 0 || field.MaxItems < 0 {
		return fmt.Errorf("command %q field %q has a negative limit", commandID, path)
	}
	if field.Pattern != "" {
		if _, err := regexp.Compile(field.Pattern); err != nil {
			return fmt.Errorf("command %q field %q has invalid pattern: %w", commandID, path, err)
		}
	}
	if field.Minimum != nil || field.Maximum != nil {
		if field.Kind != KindInteger && field.Kind != KindNumber {
			return fmt.Errorf("command %q non-numeric field %q declares numeric bounds", commandID, path)
		}
		if err := validateBounds(commandID, path, field.Minimum, field.Maximum); err != nil {
			return err
		}
	}
	seen := make(map[string]struct{}, len(field.Properties))
	for _, property := range field.Properties {
		propertyPath := path + "." + property.Name
		if !fieldPattern.MatchString(property.Name) {
			return fmt.Errorf("command %q has invalid field name %q", commandID, propertyPath)
		}
		if _, exists := seen[property.Name]; exists {
			return fmt.Errorf("command %q has duplicate field %q", commandID, propertyPath)
		}
		seen[property.Name] = struct{}{}
		if err := validateField(commandID, propertyPath, property); err != nil {
			return err
		}
	}
	if field.Items != nil {
		if field.Kind != KindArray {
			return fmt.Errorf("command %q non-array field %q declares items", commandID, path)
		}
		if err := validateValueSpec(commandID, path+"[]", *field.Items); err != nil {
			return err
		}
	}
	return nil
}

func validateBounds(commandID, path string, minimum, maximum *string) error {
	var min, max *big.Rat
	if minimum != nil {
		var ok bool
		min, ok = new(big.Rat).SetString(*minimum)
		if !ok {
			return fmt.Errorf("command %q field %q has invalid minimum %q", commandID, path, *minimum)
		}
	}
	if maximum != nil {
		var ok bool
		max, ok = new(big.Rat).SetString(*maximum)
		if !ok {
			return fmt.Errorf("command %q field %q has invalid maximum %q", commandID, path, *maximum)
		}
	}
	if min != nil && max != nil && min.Cmp(max) > 0 {
		return fmt.Errorf("command %q field %q has minimum greater than maximum", commandID, path)
	}
	return nil
}

func validateValueSpec(commandID, path string, spec ValueSpec) error {
	field := FieldSpec{
		Name: path, Kind: spec.Kind, Nullable: spec.Nullable, Enum: spec.Enum,
		Pattern: spec.Pattern, Format: spec.Format, MaxBytes: spec.MaxBytes,
		MaxItems: spec.MaxItems, Items: spec.Items, Properties: spec.Properties,
		AdditionalProperties: spec.AdditionalProperties,
	}
	return validateField(commandID, path, field)
}

func validID(id string) bool {
	parts := strings.Split(id, ".")
	for _, part := range parts {
		if !namePattern.MatchString(part) {
			return false
		}
	}
	return len(parts) > 0
}

func normalizePath(path []string) []string {
	if len(path) == 1 && strings.Contains(path[0], ".") {
		path = strings.Split(path[0], ".")
	}
	result := make([]string, len(path))
	for i, part := range path {
		result[i] = strings.ToLower(strings.TrimSpace(part))
	}
	return result
}

func hasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func cloneCommand(command CommandSpec) CommandSpec {
	command.Path = append([]string(nil), command.Path...)
	command.Params.Fields = cloneFields(command.Params.Fields)
	command.Params.OutputControls = append([]string(nil), command.Params.OutputControls...)
	command.Output.Formats = append([]string(nil), command.Output.Formats...)
	command.Output.ResponseFields = append([]string(nil), command.Output.ResponseFields...)
	command.Examples = append([]Example(nil), command.Examples...)
	command.Response = cloneValue(command.Response)
	if command.Pagination != nil {
		pagination := *command.Pagination
		command.Pagination = &pagination
	}
	return command
}

func cloneFields(fields []FieldSpec) []FieldSpec {
	result := make([]FieldSpec, len(fields))
	for i, field := range fields {
		field.Enum = append([]string(nil), field.Enum...)
		if field.Minimum != nil {
			minimum := *field.Minimum
			field.Minimum = &minimum
		}
		if field.Maximum != nil {
			maximum := *field.Maximum
			field.Maximum = &maximum
		}
		field.Properties = cloneFields(field.Properties)
		if field.Items != nil {
			item := cloneValue(*field.Items)
			field.Items = &item
		}
		result[i] = field
	}
	return result
}

func cloneValue(value ValueSpec) ValueSpec {
	value.Enum = append([]string(nil), value.Enum...)
	value.Properties = cloneFields(value.Properties)
	if value.Items != nil {
		item := cloneValue(*value.Items)
		value.Items = &item
	}
	return value
}
