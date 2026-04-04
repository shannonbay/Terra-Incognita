// Package worldengine is the World Model Engine library.
//
// World authors define arbitrary simulation worlds using this package.
// Agents are dropped into these worlds blind and must discover the rules,
// form strategies, and survive — creating an open-ended AGI benchmark.
//
// Usage:
//
//	import we "github.com/shannonbay/terra-incognita/engine/worldengine"
//
//	func main() {
//	    w := we.New(we.Config{DT: 1.0, MaxTicks: 365})
//	    // define types, spawn entities, run
//	    w.Run()
//	}
package worldengine
