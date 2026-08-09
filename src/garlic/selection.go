package garlic

// Topology-aware circuit hop selection (Sybil mitigation): nothing in
// CreateCircuit itself validates path quality - it takes whatever hop
// list the caller supplies (docs/garlic-threat-model.md, "Route
// manipulation"). SelectDiversePath is an optional helper a caller can
// use instead of hand-picking hops: given a pool of discovered/verified
// candidates (each already annotated with its mesh hop count and
// immediate tree parent - see manager.go's HopCount and core.Core.GetTree),
// it greedily prefers hops that are farther away and avoids picking two
// hops that share a tree parent (a cheap, explainable signal that they
// might be run by the same operator or sit on the same local segment).
//
// This does not solve Sybil resistance - an adversary who deploys nodes
// with genuinely diverse tree positions defeats this heuristic entirely,
// and it has no concept of IP/ASN diversity (deliberately: propagating a
// hop's real IP through gossip would itself be a privacy cost for relay
// operators, see docs/garlic-threat-model.md's Sybil section) - but it
// raises the bar above "pick uniformly at random" or "pick whatever
// answered first" for the common case of a few nearby colluding nodes.

import "errors"

var ErrInsufficientDiverseCandidates = errors.New("garlic: not enough topologically diverse candidates")

// HopCandidate is one candidate for SelectDiversePath, combining a
// discovered peer's identity with topology data about it.
type HopCandidate struct {
	NodeKey         []byte
	GarlicPublicKey []byte
	HopCount        int
	TreeParent      []byte // this candidate's immediate parent in core.Core.GetTree(), if known
}

// SelectDiversePath greedily selects n candidates from pool: sorted by
// descending HopCount (farther/more topologically distant preferred),
// skipping any candidate whose TreeParent matches an already-selected
// candidate's TreeParent. A candidate with an empty/unknown TreeParent
// never conflicts with anything (missing data isn't evidence of a shared
// parent). Candidates with HopCount below minHopCount are excluded
// entirely. Returns ErrInsufficientDiverseCandidates if fewer than n
// candidates can be selected under these constraints.
func SelectDiversePath(pool []HopCandidate, n, minHopCount int) ([]HopCandidate, error) {
	candidates := make([]HopCandidate, 0, len(pool))
	for _, c := range pool {
		if c.HopCount >= minHopCount {
			candidates = append(candidates, c)
		}
	}
	sortByHopCountDescending(candidates)

	selected := make([]HopCandidate, 0, n)
	usedParents := make(map[string]bool, n)
	for _, c := range candidates {
		if len(selected) == n {
			break
		}
		parentKey := string(c.TreeParent)
		if parentKey != "" && usedParents[parentKey] {
			continue
		}
		selected = append(selected, c)
		if parentKey != "" {
			usedParents[parentKey] = true
		}
	}
	if len(selected) < n {
		return nil, ErrInsufficientDiverseCandidates
	}
	return selected, nil
}

// sortByHopCountDescending is a small insertion sort - candidate pools
// for a circuit are expected to be small (dozens, not thousands), so
// there's no need for anything fancier.
func sortByHopCountDescending(c []HopCandidate) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].HopCount > c[j-1].HopCount; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}
