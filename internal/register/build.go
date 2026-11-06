package register

// Input is what the builder reads: the seat and the duties its role declares.
// The builder reads values and checks nothing itself.
type Input struct {
	Seat   string
	Duties []string
}

// Build makes the row for a seat. It reads no check, so every cell it makes is
// unmeasured: never observed, with the source that says so. A later step fills
// a cell from its own reading.
func Build(in Input) Row {
	row := Row{
		Seat: in.Seat,
		Role: unmeasured(), Team: unmeasured(), Harness: unmeasured(), Host: unmeasured(),
		Alive: unmeasured(), PaneAlive: unmeasured(),
		HarnessVersion: unmeasured(), HarnessCovered: unmeasured(), HarnessReader: unmeasured(),
		InterruptKey: unmeasured(), EmptyCCExits: unmeasured(), BusPublishScope: unmeasured(),
	}
	if len(in.Duties) > 0 {
		row.Duties = make(map[string]Cell, len(in.Duties))
		for _, d := range in.Duties {
			row.Duties[d] = unmeasured()
		}
	}
	return row
}

func unmeasured() Cell { return Cell{Source: SourceUnmeasured} }
