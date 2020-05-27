package main

import (
	"bufio"
	"fmt"
	"github.com/alecthomas/kong"
	"github.com/teivah/bitvector"
	"io"
	"log"
	"math"
	"os"
	"strings"
	"time"
)

// Solver for IBM Ponder This - April 2020 (COVID-19 outbreak) challenge.
// See https://quaxio.com/ponder_this_april_2020_writeup/ for writeup.

var args struct {
	Compute struct {
		Algorithm string `help:"\"recursive\" or \"dp\""`
		Graph string `required help:"comma separated rows, e.g. \"011,100,010\""`
		Rate float64 `default:"0.10" help:"daily probability for infection to pass between edges"`
		Days uint `required help:"number of days to compute"`
	} `cmd help:"Compute probability for a given graph."`

	Solve struct {
		Algorithm string `help:"\"recursive\" or \"dp\""`
		Graphs string `required type:"path" help:"pre-computed list of graphs to solve with"`
		Target float64 `default:"0.70" help:"target probability to solve for"`
		Rate float64 `default:"0.10" help:"daily probability for infection to pass between edges"`
		Days uint `required help:"number of days to solve for"`
	} `cmd help:"Search for a solution."`
}

type graph struct {
	size     uint8 // number of vertices
	vertices bitvector.Len64
}

type stateProbability struct {
	state       bitvector.Len8
	probability float64
}

func main() {
	ctx := kong.Parse(&args)
	switch ctx.Command() {
	case "compute":
		// Parse graph
		g := parseMatrix(args.Compute.Graph)
		r := compute(g, args.Compute.Algorithm, args.Compute.Days, args.Compute.Rate, true)
		fmt.Printf("probability of all vertices infected after %d days: %g%%\n", args.Compute.Days, r[0] * 100.0)
	case "solve":
		solve()
	default:
		panic(ctx.Command())
	}
}

// Parses an adjacency matrix into a graph
func parseMatrix(matrix string) graph {
	rows := strings.Split(matrix, ",")
	// check that we have at most 8 rows/cols
	if len(rows) > 8 {
		log.Panicf("matrix size is too large: %d > 8", len(rows))
	}

	g := graph{size: uint8(len(rows))}

	// check that we have a square matrix + convert string to bits
	for i, row := range rows {
		if len(row) != len(rows) {
			log.Panicf("row %d has length %d but expecting %d", i, len(row), len(rows))
		}
		for j, char := range row {
			switch char {
			case '0':
			case '1': g.addEdge(uint8(i), uint8(j))
			default:
				log.Panicf("unknown character in matrix: '%c'", char)
			}
		}
	}

	return g
}

func (g *graph) addEdge(vertex1, vertex2 uint8) {
	g.vertices = g.vertices.Set(vertex1*8+vertex2, true)
}

func (g *graph) hasEdge(vertex1, vertex2 uint8) bool {
	return g.vertices.Get(vertex1*8 + vertex2)
}

// Compute probability for all vertices to be infected.
func compute(g graph, algorithm string, days uint, rate float64, firstResultOnly bool) []float64 {
	// Compute probability
	switch algorithm {
	case "recursive":
		return g.computeRecursive(days, rate, firstResultOnly)
	case "dp":
		return g.computeDP(days, rate, firstResultOnly)
	default:
		panic(fmt.Sprintf("unknown algorithm: %s", algorithm))
	}
}

// Use a recursive function (note: this is going to be slow)
func (g *graph) computeRecursive(days uint, rate float64, firstResultOnly bool) []float64 {
	var r []float64
	for i:=uint8(0); i<g.size; i++ {
		// initial state is one vertex is infected on day 0.
		var state bitvector.Len8
		state = state.Set(i, true)
		r = append(r, g._computeRecursive(days, rate, state))
		if firstResultOnly {
			break
		}
	}
	return r
}

func (g *graph) _computeRecursive(days uint, rate float64, state bitvector.Len8) float64 {
	if state.Count() == g.size {
		// all vertices were infected, stop further processing
		return 1.0
	}
	if days == 0 {
		// some vertices were not infected, but we reached the end of our iterations
		return 0.0
	}

	// enumerate combinations of edges which can change state
	r := 0.0
	nextStates := g.enumerateNextStates(state, rate, 0)
	for _, nextState := range nextStates {
		r += g._computeRecursive(days-1, rate, nextState.state) * nextState.probability
	}
	return r
}

// For a given state, returns all possible next states and their probability of happening
func (g *graph) enumerateNextStates(state bitvector.Len8, rate float64, index uint8) []stateProbability {
	if index == g.size {
		return []stateProbability{{state: state, probability: 1.0}}
	}
	// if index is infected, there's nothing to do for this vertex
	if state.Get(index) {
		return g.enumerateNextStates(state, rate, index+1)
	}
	// count how many neighbors this vertex has
	infected := 0
	for i:=uint8(0); i<g.size; i++ {
		if g.hasEdge(index, i) && state.Get(i) {
			infected++
		}
	}
	if infected == 0 {
		// there are no infected neighbors
		return g.enumerateNextStates(state, rate, index+1)
	}

	// The probability of not being infected is (1-rate)^infected.
	// The probability of getting infected is 1 - (1-rate)^infected.
	p := math.Pow(1.0 - rate, float64(infected))
	r := g.enumerateNextStates(state, rate, index+1)
	var r2 []stateProbability
	for _, s := range r {
		r2 = append(r2, stateProbability{state: s.state, probability: s.probability * p})
		r2 = append(r2, stateProbability{state: s.state.Set(index, true), probability: s.probability * (1.0 - p)})
	}
	return r2
}

// Iterate through graphs and find which ones are valid solutions
func solve() {
	// Use a database of graphs to reduce search space
	file, err := os.Open(args.Solve.Graphs)
	defer file.Close()
	if err != nil {
		log.Panic(err)
	}
	// count number of lines
	fileScanner := bufio.NewScanner(file)
	lineCount := 0
	for fileScanner.Scan() {
		lineCount++
	}
	if _, err = file.Seek(0, 0); err != nil {
		log.Panic(err)
	}

	// read each graph
	reader := bufio.NewReader(file)
	startTime := time.Now()
	linesProcessed := 0
	bestValue := float64(0)
	var bestGraph graph
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Panic(err)
		}
		line = strings.TrimSuffix(line, "\n")
		g := parseMatrix(line)

		r := compute(g, args.Solve.Algorithm, args.Solve.Days, args.Solve.Rate, false)
		for i, v := range r {
			if math.Abs(v-args.Solve.Target) < math.Abs(bestValue-args.Solve.Target) && math.Abs(v-args.Solve.Target) < 0.00005 {
				fmt.Printf("Improved solution! v=%g\n", v)
				bestValue = v
				bestGraph = graph{size: g.size, vertices: g.vertices}
				bestGraph.pivot(uint8(i))
				fmt.Println(bestGraph)
			}
		}
		linesProcessed++
		timeLeft := float64(time.Now().Sub(startTime).Milliseconds()) / float64(linesProcessed) * float64(lineCount - linesProcessed)
		fmt.Printf("best: %g, eta: %s\n", bestValue, time.Duration(timeLeft)*time.Millisecond)
	}
	fmt.Println("best solution")
	fmt.Println(bestGraph)
}

// Transform g.vertices so that infected vertex becomes the first vertex.
func (g *graph) pivot(infected uint8) {
	// swap 0 and infected
	original := graph{size: g.size, vertices: g.vertices}
	g.vertices = 0
	for i := uint8(0); i < g.size; i++ {
		for j := uint8(0); j < g.size; j++ {
			if original.hasEdge(i, j) {
				mapper := func(x uint8) uint8 {
					if x == infected {
						return 0
					} else if x < infected {
						return x + 1
					} else {
						return x
					}
				}
				g.addEdge(mapper(i), mapper(j))
			}
		}
	}
}

func (g graph) String() string {
	var r strings.Builder
	for i := byte(0); i < g.size; i++ {
		for j := byte(0); j < g.size; j++ {
			if g.hasEdge(i, j) {
				fmt.Fprintf(&r, "1")
			} else {
				fmt.Fprintf(&r, "0")
			}
		}
		fmt.Fprintln(&r,"")
	}
	return r.String()
}

// Compute using dynamic programming.
func (g *graph) computeDP(days uint, rate float64, firstResultOnly bool) []float64 {
	lastState := (1 << g.size)-1

	// Build a table with 256 * (days+1) entries. We could actually make this smaller (lastState * days-1) but
	// the size we picked is a little more convenient.
	probs := make([][256]float64, days + 1)

	// fill the base case
	for state:=0; state<lastState; state++ {
		probs[0][state] = 0.0
	}
	probs[0][lastState] = 1.0

	// compute the mapping of state => nextStates
	m := make(map[int][]stateProbability)
	for state:=0; state<=lastState; state++ {
		m[state] = g.enumerateNextStates(bitvector.Len8(state), rate, 0)
	}

	// fill probs table
	for i := uint(1); i<=days; i++ {
		// each state depends on probabilities available in m and probs table
		for state:=0; state<=lastState; state++ {
			p := 0.0
			for _, nextState := range m[state] {
				p += nextState.probability * probs[i - 1][nextState.state]
			}
			probs[i][state] = p
		}
	}

	// for each possible initial state, perform a single lookup
	var r []float64
	for i := uint8(0); i < g.size; i++ {
		var initialState bitvector.Len8
		initialState = initialState.Set(i, true)
		p := probs[days][initialState]
		r = append(r, p)
		if firstResultOnly {
			break
		}
	}
	return r
}
