package contract

const (
	SuccessSchemaVersion = "pmx.response/v1"
	ErrorSchemaVersion   = "pmx.error/v1"
	NDJSONSchemaVersion  = "pmx.ndjson/v1"
)

type Pagination struct {
	PagesFetched int    `json:"pagesFetched,omitempty"`
	ItemsEmitted int    `json:"itemsEmitted"`
	Complete     bool   `json:"complete"`
	NextCursor   string `json:"nextCursor,omitempty"`
}

type Truncation struct {
	Path         string `json:"path"`
	Reason       string `json:"reason"`
	SourceCount  *int   `json:"sourceCount,omitempty"`
	EmittedCount int    `json:"emittedCount"`
}

type Meta struct {
	Provider   string       `json:"provider,omitempty"`
	Effects    Effects      `json:"effects"`
	Pagination *Pagination  `json:"pagination,omitempty"`
	Truncation []Truncation `json:"truncation,omitempty"`
	Operation  *Operation   `json:"operation,omitempty"`
}

type Operation struct {
	OperationID     string `json:"operationId"`
	ClientRequestID string `json:"clientRequestId,omitempty"`
	Status          string `json:"status"`
}

type SuccessEnvelope struct {
	SchemaVersion string `json:"schemaVersion"`
	OK            bool   `json:"ok"`
	Command       string `json:"command"`
	Data          any    `json:"data"`
	Meta          Meta   `json:"meta"`
}

type ErrorEnvelope struct {
	SchemaVersion string `json:"schemaVersion"`
	OK            bool   `json:"ok"`
	Command       string `json:"command,omitempty"`
	Error         *Error `json:"error"`
}

func Success(command string, data any, meta Meta) SuccessEnvelope {
	return SuccessEnvelope{
		SchemaVersion: SuccessSchemaVersion, OK: true, Command: command, Data: data, Meta: meta,
	}
}

func Failure(command string, err error) ErrorEnvelope {
	return ErrorEnvelope{
		SchemaVersion: ErrorSchemaVersion, OK: false, Command: command, Error: AsError(err),
	}
}

type NDJSONRecord struct {
	SchemaVersion string         `json:"schemaVersion"`
	Type          string         `json:"type"`
	Command       string         `json:"command"`
	Sequence      *int           `json:"sequence,omitempty"`
	Data          any            `json:"data,omitempty"`
	Pagination    *Pagination    `json:"pagination,omitempty"`
	Truncation    []Truncation   `json:"truncation,omitempty"`
	Error         *Error         `json:"error,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
}
