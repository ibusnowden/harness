package transcript

type Store struct {
	Entries []string
	Flushed bool
}

func (s *Store) Append(entry string) {
	s.Entries = append(s.Entries, entry)
	s.Flushed = false
}

func (s *Store) Compact(keepLast int) {
	if keepLast < 0 {
		keepLast = 0
	}
	if len(s.Entries) > keepLast {
		s.Entries = append([]string(nil), s.Entries[len(s.Entries)-keepLast:]...)
	}
}

func (s Store) Replay() []string {
	return append([]string(nil), s.Entries...)
}

func (s *Store) Flush() {
	s.Flushed = true
}
