package daemon

import (
	"log"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/resumejournal"
)

// loadResumeJournal keeps the resume journal's JSON mirror beside the socket
// (docs/design/fleet-hibernate.md section 5). At start it writes the mirror
// from what bolt holds, so a mirror left by an earlier run never disagrees with
// the store, and it installs the hook that rewrites the mirror after every
// entry written. A TCP listener has no file beside it, so it has no mirror.
//
// A mirror that cannot be written is logged and nothing else: the journal in
// bolt is the record, and the mirror is the copy a person reads with the daemon
// down.
func (d *Daemon) loadResumeJournal(socketPath string) {
	d.resumeMirror = resumejournal.MirrorPath(socketPath)
	if d.resumeMirror == "" {
		return
	}
	path := d.resumeMirror
	d.store.SetResumeJournalHook(func(entries []api.ResumeEntry) {
		if err := resumejournal.WriteMirror(path, entries); err != nil {
			log.Printf("resume journal: %v", err)
		}
	})
	if d.store.BoltPath() == "" {
		return
	}
	entries, err := d.store.ResumeJournal()
	if err != nil {
		log.Printf("resume journal: read the stored journal: %v; the mirror is left as it was", err)
		return
	}
	if err := resumejournal.WriteMirror(path, entries); err != nil {
		log.Printf("resume journal: %v", err)
	}
}
