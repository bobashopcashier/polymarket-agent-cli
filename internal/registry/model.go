package registry

import (
	"context"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

const (
	CommandIndexSchemaVersion = "pmx.command-index/v1"
	CommandSchemaVersion      = "pmx.command-schema/v1"
)

type FieldKind string

const (
	KindString  FieldKind = "string"
	KindBoolean FieldKind = "boolean"
	KindInteger FieldKind = "integer"
	KindNumber  FieldKind = "number"
	KindObject  FieldKind = "object"
	KindArray   FieldKind = "array"
)

type NormalizeMode string

const (
	NormalizeNone      NormalizeMode = "none"
	NormalizeTrim      NormalizeMode = "trim"
	NormalizeLowercase NormalizeMode = "lowercase"
	NormalizeUppercase NormalizeMode = "uppercase"
)

// FieldSpec is the authoritative request field definition used by help,
// convenience flags, raw JSON validation, and runtime schema output.
type FieldSpec struct {
	Name                 string        `json:"name"`
	Flag                 string        `json:"flag,omitempty"`
	Kind                 FieldKind     `json:"type"`
	Required             bool          `json:"required"`
	Nullable             bool          `json:"nullable,omitempty"`
	Default              any           `json:"default,omitempty"`
	Enum                 []string      `json:"enum,omitempty"`
	Pattern              string        `json:"pattern,omitempty"`
	Format               string        `json:"format,omitempty"`
	Minimum              *string       `json:"minimum,omitempty"`
	Maximum              *string       `json:"maximum,omitempty"`
	MaxBytes             int           `json:"maxBytes,omitempty"`
	MaxItems             int           `json:"maxItems,omitempty"`
	Normalize            NormalizeMode `json:"normalize,omitempty"`
	Description          string        `json:"description,omitempty"`
	Items                *ValueSpec    `json:"items,omitempty"`
	Properties           []FieldSpec   `json:"properties,omitempty"`
	AdditionalProperties bool          `json:"additionalProperties,omitempty"`
}

type ValueSpec struct {
	Kind                 FieldKind   `json:"type"`
	Nullable             bool        `json:"nullable,omitempty"`
	Enum                 []string    `json:"enum,omitempty"`
	Pattern              string      `json:"pattern,omitempty"`
	Format               string      `json:"format,omitempty"`
	MaxBytes             int         `json:"maxBytes,omitempty"`
	MaxItems             int         `json:"maxItems,omitempty"`
	Items                *ValueSpec  `json:"items,omitempty"`
	Properties           []FieldSpec `json:"properties,omitempty"`
	AdditionalProperties bool        `json:"additionalProperties,omitempty"`
}

type ObjectSpec struct {
	MaximumBytes         int         `json:"maximumBytes"`
	AdditionalProperties bool        `json:"additionalProperties"`
	ConflictRule         string      `json:"conflictRule"`
	OutputControls       []string    `json:"outputControls"`
	Fields               []FieldSpec `json:"fields"`
}

type AuthMode string

const (
	AuthNone      AuthMode = "none"
	AuthCLOBRead  AuthMode = "clob_read"
	AuthCLOBTrade AuthMode = "clob_trade"
	AuthSigner    AuthMode = "signer"
)

type AuthSpec struct {
	Mode            AuthMode `json:"mode"`
	ProfileRequired bool     `json:"profileRequired"`
}

type ConfirmationMode string

const (
	ConfirmationNone     ConfirmationMode = "none"
	ConfirmationPlanHash ConfirmationMode = "plan_hash"
)

type EffectSpec struct {
	Effects      contract.Effects `json:"effects"`
	DryRun       bool             `json:"dryRun"`
	Preflight    bool             `json:"preflight"`
	Confirmation ConfirmationMode `json:"confirmation"`
	Idempotent   bool             `json:"idempotent"`
}

type OutputSpec struct {
	Collection                bool     `json:"collection"`
	Formats                   []string `json:"formats"`
	ResponseFields            []string `json:"responseFields,omitempty"`
	DefaultItemLimit          int      `json:"defaultItemLimit,omitempty"`
	HardItemLimit             int      `json:"hardItemLimit,omitempty"`
	MaximumProviderBytes      int64    `json:"maximumProviderBytes"`
	MaximumEncodedOutputBytes int64    `json:"maximumEncodedOutputBytes"`
}

type PaginationSpec struct {
	Kind            string `json:"kind"`
	CursorField     string `json:"cursorField,omitempty"`
	DefaultMaxItems int    `json:"defaultMaxItems"`
	HardMaxItems    int    `json:"hardMaxItems"`
	DefaultMaxPages int    `json:"defaultMaxPages"`
	HardMaxPages    int    `json:"hardMaxPages"`
}

type Example struct {
	Summary string `json:"summary,omitempty"`
	Command string `json:"command"`
	Params  any    `json:"params,omitempty"`
}

// InvocationControl describes a CLI-owned control that is applied outside the
// provider request. Controls are intentionally separate from Params.Fields.
type InvocationControl struct {
	Name          string    `json:"name"`
	Type          FieldKind `json:"type"`
	Format        string    `json:"format,omitempty"`
	Default       any       `json:"default,omitempty"`
	MaximumBytes  int       `json:"maximumBytes,omitempty"`
	Maximum       string    `json:"maximum,omitempty"`
	Requires      []string  `json:"requires,omitempty"`
	ConflictsWith []string  `json:"conflictsWith,omitempty"`
	Description   string    `json:"description"`
}

type Handler func(context.Context, any) (data any, meta contract.Meta, err error)

// CommandSpec is immutable after Registry construction. NewRequest must return
// a fresh pointer to the concrete request type on every call.
type CommandSpec struct {
	ID             string          `json:"id"`
	Path           []string        `json:"path"`
	Summary        string          `json:"summary"`
	AgentInvocable bool            `json:"agentInvocable"`
	Params         ObjectSpec      `json:"params"`
	Response       ValueSpec       `json:"response"`
	Auth           AuthSpec        `json:"auth"`
	Effects        EffectSpec      `json:"effects"`
	Output         OutputSpec      `json:"output"`
	Pagination     *PaginationSpec `json:"pagination,omitempty"`
	Examples       []Example       `json:"examples,omitempty"`
	NewRequest     func() any      `json:"-"`
	Handler        Handler         `json:"-"`
}

type IndexEntry struct {
	ID             string     `json:"id"`
	Path           []string   `json:"path"`
	Summary        string     `json:"summary"`
	AgentInvocable bool       `json:"agentInvocable"`
	Auth           AuthSpec   `json:"auth"`
	Effects        EffectSpec `json:"effects"`
}

type IndexDocument struct {
	SchemaVersion      string              `json:"schemaVersion"`
	CLIVersion         string              `json:"cliVersion"`
	Prefix             []string            `json:"prefix"`
	InvocationControls []InvocationControl `json:"invocationControls"`
	Commands           []IndexEntry        `json:"commands"`
}

type SchemaDocument struct {
	SchemaVersion      string              `json:"schemaVersion"`
	CLIVersion         string              `json:"cliVersion"`
	ID                 string              `json:"id"`
	Path               []string            `json:"path"`
	Summary            string              `json:"summary"`
	AgentInvocable     bool                `json:"agentInvocable"`
	Params             ObjectSpec          `json:"params"`
	Response           ValueSpec           `json:"response"`
	Auth               AuthSpec            `json:"auth"`
	Effects            EffectSpec          `json:"effects"`
	Output             OutputSpec          `json:"output"`
	Pagination         *PaginationSpec     `json:"pagination,omitempty"`
	ErrorCodes         []string            `json:"errorCodes"`
	InvocationControls []InvocationControl `json:"invocationControls"`
	Examples           []Example           `json:"examples,omitempty"`
}
