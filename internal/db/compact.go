package db

// ReclaimableBytes is the space that deleted rows left free inside the database file (free pages):
// what compacting it (Compact) would give back to the disk.
func (s *Store) ReclaimableBytes() (int64, error) {
	var free, size int64
	if err := s.reader().QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		return 0, err
	}
	if err := s.reader().QueryRow(`PRAGMA page_size`).Scan(&size); err != nil {
		return 0, err
	}
	return free * size, nil
}

// RunningRuns counts the runs still in progress.
func (s *Store) RunningRuns() (int, error) {
	var n int
	err := s.reader().QueryRow(`SELECT COUNT(*) FROM runs WHERE status = 'RUNNING'`).Scan(&n)
	return n, err
}
