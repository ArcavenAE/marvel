package register

// Input is what the builder reads: the seat and the duties its role declares.
// The builder reads values and checks nothing itself.
type Input struct {
	Seat   string
	Duties []string
}

// Build makes the row for a seat.
func Build(in Input) Row { return Row{} }
