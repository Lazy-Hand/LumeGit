package git

// GraphLine is one row of the commit graph, computed from the log's
// parent relationships (like `git log --graph` but structured).
//
// Lanes: each commit occupies a vertical lane; merge commits branch
// left→right, fork points merge right→left. We assign each OID a lane
// on first sight, and a commit's parents decide which lanes continue.
type GraphLine struct {
	// Lanes holds, for each lane column, what it draws on this row:
	// 0 = empty, 1 = vertical line, 2 = this row's commit node.
	Lanes []int
	// Lane is the column index where the commit node sits.
	Lane int
	// ColorIdx picks the commit's lane color (stable per-lane).
	ColorIdx int
	// Edges are connector segments to draw: {fromLane,toLane} curved
	// toward a parent that lives on another lane.
	Edges [][2]int
}

// BuildGraph assigns lanes to commits so the UI can draw a graph like
// IDEA's log. Input order must be newest→oldest (git log order).
func BuildGraph(commits []Commit) []GraphLine {
	type laneInfo struct {
		oid   string // the commit this lane expects next
		color int
	}
	var lanes []laneInfo
	// oid → lane it was assigned (for the node row itself).
	assigned := map[string]int{}
	lines := make([]GraphLine, len(commits))
	nextColor := 0

	freeLane := func() int {
		for i, l := range lanes {
			if l.oid == "" {
				return i
			}
		}
		lanes = append(lanes, laneInfo{})
		return len(lanes) - 1
	}

	for i, cm := range commits {
		gl := GraphLine{ColorIdx: -1}
		// Which lane expects this commit? First lane waiting for it wins.
		lane := -1
		for j, l := range lanes {
			if l.oid == cm.OID {
				lane = j
				break
			}
		}
		if lane == -1 {
			lane = freeLane()
			lanes[lane].color = nextColor % 6
			nextColor++
		}
		assigned[cm.OID] = lane
		gl.Lane = lane
		gl.ColorIdx = lanes[lane].color
		// This lane now expects the first parent (linear continuation);
		// extra parents open new lanes.
		if len(cm.Parents) == 0 {
			lanes[lane].oid = "" // lane ends here
		} else {
			lanes[lane].oid = cm.Parents[0]
			for _, p := range cm.Parents[1:] {
				// Give the extra parent its own lane (or reuse a lane
				// already expecting it — merge into an existing lane).
				pl := -1
				for j, l := range lanes {
					if l.oid == p {
						pl = j
						break
					}
				}
				if pl == -1 {
					pl = freeLane()
					lanes[pl].oid = p
					lanes[pl].color = nextColor % 6
					nextColor++
				}
				gl.Edges = append(gl.Edges, [2]int{lane, pl})
			}
		}
		// Lanes snapshot for drawing.
		n := lane + 1
		for j := range lanes {
			if lanes[j].oid != "" && j+1 > n {
				n = j + 1
			}
		}
		gl.Lanes = make([]int, n)
		for j := 0; j < n; j++ {
			if j == lane {
				gl.Lanes[j] = 2
			} else if j < len(lanes) && lanes[j].oid != "" {
				gl.Lanes[j] = 1
			}
		}
		lines[i] = gl
	}
	_ = assigned
	return lines
}
