package register

import "time"

// Input is what the builder reads: the seat and the duties its role declares.
// The builder reads values and checks nothing itself.
type Input struct {
	Seat   string
	Duties []string
	// Declared holds what the build declares for the seat's harness. A cell
	// with no value here stays unmeasured.
	Declared Declared
}

// Declared is the build-derived values of a row (design section 4): the
// harness's composer reader, the interrupt table's two entries and the bus
// publish scope. They are declared, not checked, so each prints as the word
// "declared" and not as a value.
type Declared struct {
	HarnessReader   string
	InterruptKey    string
	EmptyCCExits    string
	BusPublishScope string
	// At is when the build declared them, the declared cells' observed time.
	At time.Time
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
