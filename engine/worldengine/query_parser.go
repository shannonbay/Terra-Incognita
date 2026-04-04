package worldengine

import (
	"fmt"
	"strconv"
	"strings"
)

// ----------------------------------------------------------------------------
// AST types
// ----------------------------------------------------------------------------

// QueryAST is the parsed representation of a query string.
type QueryAST struct {
	Segments   []Segment
	Aggregator string // "", "sum", "count", "min", "max", "avg"
}

// Segment is one path step in a query.
type Segment interface {
	isSegment()
}

// SegEntities selects from the world entity set.
// If ID is "" or "*", selects all entities; otherwise selects by exact ID.
type SegEntities struct {
	ID    string      // exact entity ID, "*" = wildcard, "" = wildcard
	Preds []Predicate // optional filter predicates
}

func (SegEntities) isSegment() {}

// SegSelf selects the querying entity itself (agent-scoped queries).
type SegSelf struct{}

func (SegSelf) isSegment() {}

// SegLocation descends into the querying entity's container.
type SegLocation struct{}

func (SegLocation) isSegment() {}

// SegContains descends into the contained entities of the current set.
type SegContains struct {
	Preds []Predicate
}

func (SegContains) isSegment() {}

// SegNeighbors traverses connections from the current entity set.
type SegNeighbors struct {
	Depth    int    // 0 = default (1)
	ConnType string // "" = any connection type
	Preds    []Predicate
}

func (SegNeighbors) isSegment() {}

// SegResources accesses resource values.
// If Field is "" or "*", returns all resources; otherwise returns one field.
type SegResources struct {
	Field string
}

func (SegResources) isSegment() {}

// SegParams accesses parameter values.
type SegParams struct {
	Field string
}

func (SegParams) isSegment() {}

// SegAvailableActions returns the list of action names for an entity.
type SegAvailableActions struct{}

func (SegAvailableActions) isSegment() {}

// SegWorldConfig returns world configuration.
type SegWorldConfig struct{}

func (SegWorldConfig) isSegment() {}

// Predicate is a boolean test applied during entity filtering.
type Predicate struct {
	Key   string // "type", "resources.fuel", "resources.fish_stock", etc.
	Op    string // "=", ">", "<", ">=", "<="
	Value string // raw string (may be number or type name)
}

// ValueFloat attempts to parse Value as float64.
func (p Predicate) ValueFloat() (float64, bool) {
	f, err := strconv.ParseFloat(p.Value, 64)
	return f, err == nil
}

// ----------------------------------------------------------------------------
// Parser
// ----------------------------------------------------------------------------

// ParseQuery parses a query string into a QueryAST.
// Returns an error for malformed input.
func ParseQuery(query string) (*QueryAST, error) {
	p := &parser{s: strings.TrimSpace(query)}
	return p.parse()
}

type parser struct {
	s   string
	pos int
}

func (p *parser) parse() (*QueryAST, error) {
	ast := &QueryAST{}

	for p.pos < len(p.s) {
		ch := p.s[p.pos]

		switch {
		case ch == '/':
			p.pos++ // consume '/'
			// Allow /@aggregator syntax (spec uses this form).
			if p.pos < len(p.s) && p.s[p.pos] == '@' {
				p.pos++
				agg, err := p.parseIdent()
				if err != nil {
					return nil, fmt.Errorf("expected aggregator name after @: %w", err)
				}
				switch agg {
				case "sum", "count", "min", "max", "avg":
					ast.Aggregator = agg
				default:
					return nil, fmt.Errorf("unknown aggregator %q", agg)
				}
				break
			}
			seg, err := p.parseSegment()
			if err != nil {
				return nil, err
			}
			if seg != nil {
				ast.Segments = append(ast.Segments, seg)
			}

		case ch == '@':
			p.pos++
			agg, err := p.parseIdent()
			if err != nil {
				return nil, fmt.Errorf("expected aggregator name after @: %w", err)
			}
			switch agg {
			case "sum", "count", "min", "max", "avg":
				ast.Aggregator = agg
			default:
				return nil, fmt.Errorf("unknown aggregator %q", agg)
			}

		default:
			return nil, fmt.Errorf("unexpected character %q at position %d", ch, p.pos)
		}
	}
	return ast, nil
}

func (p *parser) parseSegment() (Segment, error) {
	if p.pos >= len(p.s) {
		return nil, nil
	}

	keyword, err := p.parseIdent()
	if err != nil {
		return nil, err
	}

	switch keyword {
	case "entities":
		return p.parseEntitiesSegment()
	case "self":
		return SegSelf{}, nil
	case "location":
		return SegLocation{}, nil
	case "contains":
		preds, err := p.parsePredList()
		if err != nil {
			return nil, err
		}
		return SegContains{Preds: preds}, nil
	case "neighbors":
		return p.parseNeighborsSegment()
	case "resources":
		return p.parseFieldSegment("resources")
	case "params":
		return p.parseFieldSegment("params")
	case "available_actions":
		return SegAvailableActions{}, nil
	case "config":
		return SegWorldConfig{}, nil
	case "*":
		// wildcard — treated as all-entities
		return SegEntities{ID: "*"}, nil
	default:
		return nil, fmt.Errorf("unknown path segment %q", keyword)
	}
}

func (p *parser) parseEntitiesSegment() (Segment, error) {
	seg := SegEntities{}

	// Check for /entities/id or /entities[preds] or /entities*
	if p.pos < len(p.s) {
		switch p.s[p.pos] {
		case '/':
			// /entities/<id-or-wildcard>
			p.pos++
			id, err := p.parseIdentOrWildcard()
			if err != nil {
				return nil, err
			}
			seg.ID = id
			// optional predicate after id: /entities/lake1[...]
			preds, err := p.parsePredList()
			if err != nil {
				return nil, err
			}
			seg.Preds = preds
		case '[':
			preds, err := p.parsePredList()
			if err != nil {
				return nil, err
			}
			seg.Preds = preds
		}
	}
	return seg, nil
}

func (p *parser) parseNeighborsSegment() (Segment, error) {
	seg := SegNeighbors{Depth: 1}

	// Optional args: (depth=N, type=T)
	if p.pos < len(p.s) && p.s[p.pos] == '(' {
		p.pos++
		for p.pos < len(p.s) && p.s[p.pos] != ')' {
			p.skipWS()
			key, err := p.parseIdent()
			if err != nil {
				return nil, err
			}
			p.skipWS()
			if p.pos >= len(p.s) || p.s[p.pos] != '=' {
				return nil, fmt.Errorf("expected '=' in neighbors arg")
			}
			p.pos++
			p.skipWS()
			val, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			switch key {
			case "depth":
				d, err := strconv.Atoi(val)
				if err != nil {
					return nil, fmt.Errorf("depth must be integer: %w", err)
				}
				seg.Depth = d
			case "type":
				seg.ConnType = val
			}
			p.skipWS()
			if p.pos < len(p.s) && p.s[p.pos] == ',' {
				p.pos++
			}
		}
		if p.pos < len(p.s) && p.s[p.pos] == ')' {
			p.pos++
		}
	}

	// Optional predicate filter
	preds, err := p.parsePredList()
	if err != nil {
		return nil, err
	}
	seg.Preds = preds
	return seg, nil
}

func (p *parser) parseFieldSegment(kind string) (Segment, error) {
	var field string
	if p.pos < len(p.s) && p.s[p.pos] == '/' {
		p.pos++
		f, err := p.parseIdentOrWildcard()
		if err != nil {
			return nil, err
		}
		field = f
	}
	if kind == "resources" {
		return SegResources{Field: field}, nil
	}
	return SegParams{Field: field}, nil
}

// parsePredList parses an optional [...] predicate block.
func (p *parser) parsePredList() ([]Predicate, error) {
	if p.pos >= len(p.s) || p.s[p.pos] != '[' {
		return nil, nil
	}
	p.pos++ // consume '['
	var preds []Predicate
	for p.pos < len(p.s) && p.s[p.pos] != ']' {
		p.skipWS()
		pred, err := p.parsePredicate()
		if err != nil {
			return nil, err
		}
		preds = append(preds, pred)
		p.skipWS()
		if p.pos < len(p.s) && p.s[p.pos] == ',' {
			p.pos++
		}
	}
	if p.pos < len(p.s) && p.s[p.pos] == ']' {
		p.pos++
	}
	return preds, nil
}

func (p *parser) parsePredicate() (Predicate, error) {
	key, err := p.parseKey()
	if err != nil {
		return Predicate{}, err
	}
	p.skipWS()
	op, err := p.parseOp()
	if err != nil {
		return Predicate{}, err
	}
	p.skipWS()
	val, err := p.parseValue()
	if err != nil {
		return Predicate{}, err
	}
	return Predicate{Key: key, Op: op, Value: val}, nil
}

// parseKey parses "type", "resources.fuel", etc.
func (p *parser) parseKey() (string, error) {
	start := p.pos
	for p.pos < len(p.s) {
		ch := p.s[p.pos]
		if ch == '=' || ch == '>' || ch == '<' || ch == ']' || ch == ',' || ch == ' ' || ch == '\t' {
			break
		}
		p.pos++
	}
	if p.pos == start {
		return "", fmt.Errorf("expected predicate key at position %d", p.pos)
	}
	return p.s[start:p.pos], nil
}

func (p *parser) parseOp() (string, error) {
	if p.pos >= len(p.s) {
		return "", fmt.Errorf("expected operator")
	}
	switch p.s[p.pos] {
	case '=':
		p.pos++
		return "=", nil
	case '>':
		p.pos++
		if p.pos < len(p.s) && p.s[p.pos] == '=' {
			p.pos++
			return ">=", nil
		}
		return ">", nil
	case '<':
		p.pos++
		if p.pos < len(p.s) && p.s[p.pos] == '=' {
			p.pos++
			return "<=", nil
		}
		return "<", nil
	}
	return "", fmt.Errorf("expected operator at position %d, got %q", p.pos, p.s[p.pos])
}

func (p *parser) parseValue() (string, error) {
	start := p.pos
	for p.pos < len(p.s) {
		ch := p.s[p.pos]
		if ch == ']' || ch == ')' || ch == ',' || ch == ' ' || ch == '\t' {
			break
		}
		p.pos++
	}
	if p.pos == start {
		return "", fmt.Errorf("expected value at position %d", p.pos)
	}
	return p.s[start:p.pos], nil
}

func (p *parser) parseIdent() (string, error) {
	start := p.pos
	for p.pos < len(p.s) {
		ch := p.s[p.pos]
		if !isIdentChar(ch) {
			break
		}
		p.pos++
	}
	if p.pos == start {
		if p.pos < len(p.s) && p.s[p.pos] == '*' {
			p.pos++
			return "*", nil
		}
		return "", fmt.Errorf("expected identifier at position %d", p.pos)
	}
	return p.s[start:p.pos], nil
}

func (p *parser) parseIdentOrWildcard() (string, error) {
	if p.pos < len(p.s) && p.s[p.pos] == '*' {
		p.pos++
		return "*", nil
	}
	return p.parseIdent()
}

func (p *parser) skipWS() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t') {
		p.pos++
	}
}

func isIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') || ch == '_' || ch == '-'
}
