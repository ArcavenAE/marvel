package bus

import "context"

// Headroom is the red commit's stub.
type Headroom struct {
	FileUsed, FileLimit uint64
	MemUsed, MemLimit   uint64
	FSFree, FSTotal     uint64
}

// Low is the stub.
func (h Headroom) Low() []string { return nil }

// Problems is the stub.
func (h Headroom) Problems() string { return "" }

// ReadHeadroom is the stub.
func ReadHeadroom(ctx context.Context, monitorAddr, storeDir string) (Headroom, error) {
	return Headroom{}, nil
}
