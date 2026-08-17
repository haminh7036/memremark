package daemon

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/haminh7036/memremark/internal/adapter/antigravity"
)

// antigravityConvKeyPrefix namespaces Antigravity CLI per-conversation idx
// watermarks in the poll_state table so they can't collide with Claude
// Code's per-file keys.
const antigravityConvKeyPrefix = "antigravity:conv:"

func antigravityConvKey(conversationID string) string {
	return antigravityConvKeyPrefix + conversationID
}

func (d *Daemon) pollAntigravity(now time.Time) error {
	// Tolerate a summaries DB that doesn't exist yet -- e.g. any machine
	// where the user has only ever used Claude Code, never Antigravity CLI.
	// Same tolerance pattern claudecode.DiscoverTranscriptFiles already uses
	// for a missing ~/.claude/projects directory: "doesn't exist" means "no
	// conversations, nothing to do", not an error to log every poll tick.
	if _, err := os.Stat(d.antigravitySummariesDB); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	convs, err := antigravity.ListConversations(d.antigravitySummariesDB)
	if err != nil {
		return err
	}
	for _, conv := range convs {
		if conv.WorkspaceURIs == "" {
			continue
		}
		dbPath := filepath.Join(filepath.Dir(d.antigravitySummariesDB), "conversations", conv.ID+".db")
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		sinceIdx, ok := d.antigravityLastIdx[conv.ID]
		if !ok {
			// First time this process has seen this conversation: fall back
			// to a persisted watermark from a previous daemon run before
			// defaulting to -1 (read everything).
			sinceIdx = -1
			if persisted, found, err := d.Store.GetPollState(antigravityConvKey(conv.ID)); err != nil {
				log.Printf("daemon: get poll state for conversation %s: %v", conv.ID, err)
			} else if found {
				sinceIdx = persisted
			}
		}
		obs, maxIdx, err := antigravity.ReadObservations(dbPath, conv.WorkspaceURIs, conv.ID, conv.LastModified, sinceIdx)
		if err != nil {
			// Task 8's code review flagged this: on a mid-scan error, maxIdx may
			// already be advanced past rows that weren't returned in obs. Do NOT
			// persist it here -- keep sinceIdx as-is so the next poll retries from
			// the last known-good position instead of silently skipping rows.
			log.Printf("daemon: read antigravity conversation %s: %v", conv.ID, err)
			continue
		}
		d.antigravityLastIdx[conv.ID] = maxIdx
		for _, o := range obs {
			if err := d.recordObservation(o, d.antigravityInvoker, now); err != nil {
				log.Printf("daemon: record observation: %v", err)
			}
		}
		// Persist the watermark only after every observation in this batch
		// has actually been written to storage -- mirroring pollClaudeCode's
		// ordering (persist after the write, not before). Otherwise a crash
		// between the persist and the writes would make a restart resume
		// past rows that were never durably recorded, silently losing them.
		if err := d.Store.SetPollState(antigravityConvKey(conv.ID), maxIdx); err != nil {
			log.Printf("daemon: persist watermark for conversation %s: %v", conv.ID, err)
		}
	}
	return nil
}
