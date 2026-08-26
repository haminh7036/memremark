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
	conversationsDir := filepath.Join(filepath.Dir(d.antigravitySummariesDB), "conversations")
	diskConvIDs, err := antigravity.DiscoverConversationDBs(conversationsDir)
	if err != nil {
		log.Printf("daemon: discover antigravity dbs: %v", err)
	}

	// Tolerate a summaries DB that doesn't exist yet -- e.g. any machine
	// where the user has only ever used Claude Code, never Antigravity CLI,
	// or where active sessions are in progress before summaries DB is created.
	info, err := os.Stat(d.antigravitySummariesDB)
	if err == nil {
		if d.antigravityConvs == nil || !info.ModTime().Equal(d.antigravitySummariesModTime) || info.Size() != d.antigravitySummariesSize {
			freshConvs, err := antigravity.ListConversations(d.antigravitySummariesDB)
			if err != nil {
				return err
			}
			d.antigravityConvs = freshConvs
			d.antigravitySummariesModTime = info.ModTime()
			d.antigravitySummariesSize = info.Size()
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// Merge indexed conversations from summaries.db with discovered .db files on disk
	seen := make(map[string]bool)
	var allConvs []antigravity.ConversationInfo

	for _, conv := range d.antigravityConvs {
		seen[conv.ID] = true
		allConvs = append(allConvs, conv)
	}

	for _, id := range diskConvIDs {
		if !seen[id] {
			seen[id] = true
			allConvs = append(allConvs, antigravity.ConversationInfo{
				ID: id,
			})
		}
	}

	if len(allConvs) == 0 {
		return nil
	}

	if d.antigravityConvWing == nil {
		d.antigravityConvWing = make(map[string]string)
	}
	if d.antigravityDBMeta == nil {
		d.antigravityDBMeta = make(map[string]dbMeta)
	}
	if d.antigravityLastIdx == nil {
		d.antigravityLastIdx = make(map[string]int64)
	}

	var knownWings []string
	var knownWingsLoaded bool
	getKnownWings := func() []string {
		if !knownWingsLoaded && d.Store != nil {
			var err error
			knownWings, err = d.Store.ListWingPaths()
			if err != nil {
				log.Printf("daemon: list wing paths: %v", err)
			}
			knownWingsLoaded = true
		}
		return knownWings
	}

	for _, conv := range allConvs {
		if d.isSummarySession(conv.ID) {
			continue
		}

		cleanWingPath := d.antigravityConvWing[conv.ID]
		if cleanWingPath == "" && conv.WorkspaceURIs != "" {
			cleanWingPath = antigravity.ExtractWorkspacePath(conv.WorkspaceURIs)
			if cleanWingPath != "" {
				d.antigravityConvWing[conv.ID] = cleanWingPath
			}
		}

		dbPath := filepath.Join(conversationsDir, conv.ID+".db")
		if cleanWingPath == "" {
			cleanWingPath = antigravity.ExtractWorkspacePathFromDB(dbPath, getKnownWings())
			if cleanWingPath != "" {
				d.antigravityConvWing[conv.ID] = cleanWingPath
			}
		}
		if cleanWingPath == "" {
			continue
		}

		dbInfo, err := os.Stat(dbPath)
		if err != nil {
			continue
		}
		meta, seenMeta := d.antigravityDBMeta[conv.ID]
		if seenMeta && !meta.modTime.IsZero() && dbInfo.ModTime().Equal(meta.modTime) && dbInfo.Size() == meta.size {
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

		lastModified := conv.LastModified
		if lastModified.IsZero() {
			lastModified = dbInfo.ModTime()
		}

		obs, maxIdx, err := antigravity.ReadObservations(dbPath, cleanWingPath, conv.ID, lastModified, sinceIdx)
		if err != nil {
			// Task 8's code review flagged this: on a mid-scan error, maxIdx may
			// already be advanced past rows that weren't returned in obs. Do NOT
			// persist it here -- keep sinceIdx as-is so the next poll retries from
			// the last known-good position instead of silently skipping rows.
			log.Printf("daemon: read antigravity conversation %s: %v", conv.ID, err)
			continue
		}
		d.antigravityDBMeta[conv.ID] = dbMeta{
			modTime: dbInfo.ModTime(),
			size:    dbInfo.Size(),
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
		if maxIdx > sinceIdx {
			if err := d.Store.SetPollState(antigravityConvKey(conv.ID), maxIdx); err != nil {
				log.Printf("daemon: persist watermark for conversation %s: %v", conv.ID, err)
			}
		}
	}
	return nil
}
