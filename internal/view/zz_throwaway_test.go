package view

import "testing"

func TestThrowawayTheMacOSJobCanFail(t *testing.T) {
	t.Fatal("deliberate failure to show the macOS job goes red (marvel#623); reverted by the next commit")
}
