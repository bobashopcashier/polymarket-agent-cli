package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/params"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/registry"
)

type globalOptions struct {
	ParamsRaw string
	HasParams bool
	Fields    string
	Compact   bool
	Execute   bool
	Timeout   time.Duration
	Help      bool
	Version   bool
}

type invocation struct {
	Command registry.CommandSpec
	Request any
	Options globalOptions
}

func parseGlobal(arguments []string) (globalOptions, []string, error) {
	options := globalOptions{Timeout: 15 * time.Second}
	remaining := make([]string, 0, len(arguments))
	seen := map[string]bool{}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			remaining = append(remaining, arguments[index:]...)
			break
		}
		name, inline, hasInline := splitFlag(argument)
		switch name {
		case "--params", "--fields", "--timeout":
			if seen[name] {
				return globalOptions{}, nil, contract.Invalid("duplicate_flag", fmt.Sprintf("%s may be supplied only once", name))
			}
			seen[name] = true
			value := inline
			if !hasInline {
				if index+1 >= len(arguments) {
					return globalOptions{}, nil, contract.Invalid("missing_flag_value", fmt.Sprintf("%s requires a value", name))
				}
				index++
				value = arguments[index]
			}
			switch name {
			case "--params":
				options.ParamsRaw, options.HasParams = value, true
			case "--fields":
				options.Fields = value
			case "--timeout":
				duration, err := time.ParseDuration(value)
				if err != nil || duration < time.Millisecond || duration > time.Minute {
					return globalOptions{}, nil, contract.Invalid("invalid_timeout", "--timeout must be between 1ms and 1m")
				}
				options.Timeout = duration
			}
		case "--compact", "--execute":
			if hasInline {
				return globalOptions{}, nil, contract.Invalid("invalid_flag", fmt.Sprintf("%s does not accept a value", name))
			}
			if seen[name] {
				return globalOptions{}, nil, contract.Invalid("duplicate_flag", fmt.Sprintf("%s may be supplied only once", name))
			}
			seen[name] = true
			if name == "--compact" {
				options.Compact = true
			} else {
				options.Execute = true
			}
		case "--json":
			if hasInline {
				return globalOptions{}, nil, contract.Invalid("invalid_flag", "--json does not accept a value")
			}
		case "--help", "-h":
			options.Help = true
		case "--version":
			options.Version = true
		default:
			remaining = append(remaining, argument)
		}
	}
	return options, remaining, nil
}

func parseInvocation(ctx context.Context, arguments []string, stdin io.Reader, commands *registry.Registry) (invocation, error) {
	if err := params.RejectArgumentControls(arguments); err != nil {
		return invocation{}, err
	}
	options, remaining, err := parseGlobal(arguments)
	if err != nil {
		return invocation{}, err
	}
	if len(remaining) == 0 {
		return invocation{}, contract.Invalid("missing_command", "expected a command path such as markets list")
	}
	command, ok := commands.Lookup(remaining[0])
	requestStart := 1
	if !ok && len(remaining) >= 2 {
		command, ok = commands.Lookup(remaining[0], remaining[1])
		requestStart = 2
	}
	if !ok {
		candidate := remaining
		if len(candidate) > 2 {
			candidate = candidate[:2]
		}
		return invocation{}, contract.Invalid("unknown_command", fmt.Sprintf("unknown command %q", strings.Join(candidate, " ")))
	}
	requestTokens := remaining[requestStart:]
	request := command.NewRequest()
	if request == nil {
		return invocation{}, contract.Internal("command registry has no request constructor", nil)
	}
	if options.HasParams {
		if err := params.EnsureParamsExclusive(true, requestTokens); err != nil {
			return invocation{}, err
		}
		raw, err := params.ReadSourceContext(ctx, options.ParamsRaw, stdin, command.Params.MaximumBytes)
		if err != nil {
			return invocation{}, err
		}
		if err := params.DecodeInto(raw, command.Params, request); err != nil {
			return invocation{}, err
		}
	} else {
		raw, err := convenienceJSON(command, requestTokens)
		if err != nil {
			return invocation{}, err
		}
		if err := params.DecodeInto(raw, command.Params, request); err != nil {
			return invocation{}, err
		}
	}
	return invocation{Command: command, Request: request, Options: options}, nil
}

func convenienceJSON(command registry.CommandSpec, arguments []string) ([]byte, error) {
	fields := make(map[string]registry.FieldSpec, len(command.Params.Fields)*2)
	for _, field := range command.Params.Fields {
		fields[field.Name] = field
		if field.Flag != "" {
			fields[strings.TrimPrefix(field.Flag, "--")] = field
		}
	}
	values := map[string]any{}
	positionals := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			positionals = append(positionals, arguments[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, "--") {
			positionals = append(positionals, argument)
			continue
		}
		name, inline, hasInline := splitFlag(argument)
		lookup := strings.TrimPrefix(name, "--")
		field, ok := fields[lookup]
		if !ok {
			return nil, contract.Invalid("unknown_flag", fmt.Sprintf("unknown request flag %q for %s", name, command.ID))
		}
		if _, duplicate := values[field.Name]; duplicate {
			return nil, contract.Invalid("duplicate_parameter", fmt.Sprintf("parameter %q was supplied more than once", field.Name))
		}
		value := inline
		if field.Kind == registry.KindBoolean && !hasInline {
			if index+1 < len(arguments) && !strings.HasPrefix(arguments[index+1], "--") {
				candidate := strings.ToLower(arguments[index+1])
				if candidate == "true" || candidate == "false" {
					index++
					value = candidate
					hasInline = true
				}
			}
			if !hasInline {
				values[field.Name] = true
				continue
			}
		}
		if !hasInline {
			if index+1 >= len(arguments) {
				return nil, contract.Invalid("missing_flag_value", fmt.Sprintf("%s requires a value", name))
			}
			index++
			value = arguments[index]
		}
		converted, err := convertFlagValue(field, value)
		if err != nil {
			return nil, err
		}
		values[field.Name] = converted
	}

	positionalNames := positionalFields(command.ID)
	if len(positionals) > len(positionalNames) {
		return nil, contract.Invalid("too_many_arguments", fmt.Sprintf("%s accepts at most %d positional request values", command.ID, len(positionalNames)))
	}
	for index, value := range positionals {
		name := positionalNames[index]
		if _, duplicate := values[name]; duplicate {
			return nil, contract.Invalid("duplicate_parameter", fmt.Sprintf("parameter %q was supplied both positionally and by flag", name))
		}
		values[name] = value
	}
	return json.Marshal(values)
}

func convertFlagValue(field registry.FieldSpec, value string) (any, error) {
	switch field.Kind {
	case registry.KindString:
		return value, nil
	case registry.KindBoolean:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, contract.Invalid("invalid_flag_value", fmt.Sprintf("--%s must be true or false", field.Flag))
		}
		return parsed, nil
	case registry.KindInteger, registry.KindNumber:
		if _, err := strconv.ParseInt(value, 10, 64); err != nil && field.Kind == registry.KindInteger {
			return nil, contract.Invalid("invalid_flag_value", fmt.Sprintf("--%s must be a whole number", field.Flag))
		}
		return json.Number(value), nil
	default:
		return nil, contract.Invalid("unsupported_flag", fmt.Sprintf("--%s must be supplied through --params", field.Flag))
	}
}

func positionalFields(commandID string) []string {
	switch commandID {
	case "markets.get", "events.get":
		return []string{"id"}
	case "markets.search":
		return []string{"q"}
	case "clob.price", "clob.midpoint", "clob.spread", "clob.book", "clob.tick-size", "clob.fee-rate", "clob.neg-risk", "clob.last-trade":
		return []string{"tokenId"}
	default:
		return nil
	}
}

func splitFlag(argument string) (name, value string, hasValue bool) {
	name, value, found := strings.Cut(argument, "=")
	return name, value, found
}
