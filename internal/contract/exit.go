package contract

// ExitCode is the stable process-exit taxonomy exposed by pmx.
type ExitCode int

const (
	ExitOK            ExitCode = 0
	ExitInternal      ExitCode = 1
	ExitInvalidInput  ExitCode = 2
	ExitAuth          ExitCode = 3
	ExitPolicy        ExitCode = 4
	ExitNotFound      ExitCode = 5
	ExitRejected      ExitCode = 6
	ExitTransient     ExitCode = 7
	ExitPartial       ExitCode = 8
	ExitIndeterminate ExitCode = 9
	ExitInterrupted   ExitCode = 130
)

func (c ExitCode) Int() int { return int(c) }
