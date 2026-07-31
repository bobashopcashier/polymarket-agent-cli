package contract

type NetworkEffect string

const (
	NetworkNone  NetworkEffect = "none"
	NetworkRead  NetworkEffect = "read"
	NetworkWrite NetworkEffect = "write"
)

type MutationKind string

const (
	MutationNone        MutationKind = "none"
	MutationOrderCreate MutationKind = "order.create"
	MutationOrderCancel MutationKind = "order.cancel"
	MutationCancelAll   MutationKind = "order.cancel_all"
	MutationApproval    MutationKind = "token.approval"
	MutationOnchain     MutationKind = "onchain.transaction"
	MutationCredential  MutationKind = "credential.write"
	MutationSignature   MutationKind = "signature.create"
)

type RiskTier string

const (
	RiskNone     RiskTier = "none"
	RiskLow      RiskTier = "low"
	RiskHigh     RiskTier = "high"
	RiskCritical RiskTier = "critical"
)

// Effects declares both static command capabilities and the realized state of
// an invocation. Executed is false for schemas, dry-runs, and preflights.
type Effects struct {
	Executed   bool          `json:"executed"`
	Network    NetworkEffect `json:"network"`
	Mutation   MutationKind  `json:"mutation"`
	Signing    bool          `json:"signing"`
	Financial  bool          `json:"financial"`
	Broadcast  bool          `json:"broadcast"`
	Reversible bool          `json:"reversible"`
	Risk       RiskTier      `json:"risk"`
}

func (e Effects) IsMutation() bool {
	return e.Mutation != "" && e.Mutation != MutationNone
}
