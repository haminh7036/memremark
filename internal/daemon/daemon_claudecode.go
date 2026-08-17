package daemon

import (
	"log"
	"time"

	"github.com/haminh7036/memremark/internal/adapter/claudecode"
)

// claudeFileKeyPrefix namespaces claudecode.Tailer byte-offset watermarks
// in the poll_state table so they can't collide with antigravity's
// per-conversation keys.
const claudeFileKeyPrefix = "claudecode:file:"

func claudeFileKey(file string) string {
	return claudeFileKeyPrefix + file
}

func (d *Daemon) pollClaudeCode(now time.Time) error {
	files, err := claudecode.DiscoverTranscriptFiles(d.claudeProjectsRoot)
	if err != nil {
		return err
	}
	for _, file := range files {
		parser, seen := d.claudeParsers[file]
		if !seen {
			parser = claudecode.NewParser()
			d.claudeParsers[file] = parser
			// First time this process has seen this file: restore any
			// watermark persisted by a previous daemon run before the first
			// ReadNewLines call, so a restart doesn't re-read the whole
			// transcript from byte 0.
			if persisted, ok, err := d.Store.GetPollState(claudeFileKey(file)); err != nil {
				log.Printf("daemon: get poll state for %s: %v", file, err)
			} else if ok {
				d.claudeTailer.SeedOffset(file, persisted)
			}
		}
		lines, err := d.claudeTailer.ReadNewLines(file)
		if err != nil {
			log.Printf("daemon: read %s: %v", file, err)
			continue
		}
		for _, line := range lines {
			obs, err := parser.Feed(line)
			if err != nil {
				log.Printf("daemon: parse %s: %v", file, err)
				continue
			}
			for _, o := range obs {
				if err := d.recordObservation(o, d.claudeInvoker, now); err != nil {
					log.Printf("daemon: record observation: %v", err)
				}
			}
		}
		if err := d.Store.SetPollState(claudeFileKey(file), d.claudeTailer.Offset(file)); err != nil {
			log.Printf("daemon: persist offset for %s: %v", file, err)
		}
	}
	return nil
}
