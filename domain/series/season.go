package series

type Season struct {
	Number    int
	Monitored bool
	Episodes  []Episode
}

func (s Season) Missing() []Episode {
	out := make([]Episode, 0, len(s.Episodes))
	for _, e := range s.Episodes {
		if e.Monitored && !e.HasFile {
			out = append(out, e)
		}
	}
	return out
}

func (s Season) Have() []Episode {
	out := make([]Episode, 0, len(s.Episodes))
	for _, e := range s.Episodes {
		if e.HasFile {
			out = append(out, e)
		}
	}
	return out
}

func (s Season) MissingNumbers() []int {
	miss := s.Missing()
	out := make([]int, 0, len(miss))
	for _, e := range miss {
		out = append(out, e.Number)
	}
	return out
}

func (s Season) HaveNumbers() []int {
	have := s.Have()
	out := make([]int, 0, len(have))
	for _, e := range have {
		out = append(out, e.Number)
	}
	return out
}
