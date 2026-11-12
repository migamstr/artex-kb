package nuclei

import (
	"context"
	"log"
	"os/exec"
	"time"
)

const syncInterval = 24 * time.Hour

type Syncer struct{ idx *Index }

func NewSyncer(idx *Index) *Syncer { return &Syncer{idx: idx} }

func (s *Syncer) Run(ctx context.Context) {
	next := nextMidnight()
	log.Printf("[nuclei-sync] first sync at %s", next.Format("2006-01-02 15:04:05"))
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			s.PullNow()
			next = next.Add(syncInterval)
		}
	}
}

func (s *Syncer) PullNow() error {
	dir := s.idx.Dir()
	log.Printf("[nuclei-sync] git pull %s", dir)
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only", "--quiet")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[nuclei-sync] pull failed: %v\n%s", err, out)
		return err
	}
	s.idx.Rebuild()
	tc, tg, at := s.idx.Stats()
	log.Printf("[nuclei-sync] index ready: %d templates %d tags (%s)", tc, tg, at.Format(time.RFC3339))
	return nil
}

func nextMidnight() time.Time {
	now := time.Now()
	y, m, d := now.Date()
	return time.Date(y, m, d+1, 0, 5, 0, 0, now.Location())
}
