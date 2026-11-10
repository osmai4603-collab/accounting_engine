package taxonomy

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/shopspring/decimal"
)

const (
	maxFormulaLength = 4096
	maxFormulaDepth  = 64
)

type formulaParser struct {
	input  []rune
	pos    int
	depth  int
	values map[string]decimal.Decimal
	refs   []string
}

func evaluateFormula(expression string, values map[string]decimal.Decimal) (decimal.Decimal, error) {
	if len(expression) > maxFormulaLength {
		return decimal.Zero, fmt.Errorf("formula exceeds %d characters", maxFormulaLength)
	}
	parser := &formulaParser{input: []rune(expression), values: values}
	result, err := parser.parseExpression()
	if err != nil {
		return decimal.Zero, err
	}
	parser.skipSpaces()
	if parser.pos != len(parser.input) {
		return decimal.Zero, fmt.Errorf("unexpected character %q", parser.input[parser.pos])
	}
	return result, nil
}

func formulaReferences(expression string) []string {
	parser := &formulaParser{input: []rune(expression)}
	for parser.pos < len(parser.input) {
		if unicode.IsLetter(parser.input[parser.pos]) || parser.input[parser.pos] == '_' {
			if identifier, ok := parser.readIdentifier(); ok {
				parser.refs = append(parser.refs, identifier)
			}
			continue
		}
		parser.pos++
	}
	return parser.refs
}

func (p *formulaParser) parseExpression() (decimal.Decimal, error) {
	value, err := p.parseTerm()
	if err != nil {
		return decimal.Zero, err
	}
	for {
		p.skipSpaces()
		if p.consume('+') {
			right, err := p.parseTerm()
			if err != nil {
				return decimal.Zero, err
			}
			value = value.Add(right)
		} else if p.consume('-') {
			right, err := p.parseTerm()
			if err != nil {
				return decimal.Zero, err
			}
			value = value.Sub(right)
		} else {
			return value, nil
		}
	}
}

func (p *formulaParser) parseTerm() (decimal.Decimal, error) {
	value, err := p.parseFactor()
	if err != nil {
		return decimal.Zero, err
	}
	for {
		p.skipSpaces()
		if p.consume('*') {
			right, err := p.parseFactor()
			if err != nil {
				return decimal.Zero, err
			}
			value = value.Mul(right)
		} else if p.consume('/') {
			right, err := p.parseFactor()
			if err != nil {
				return decimal.Zero, err
			}
			if right.IsZero() {
				return decimal.Zero, fmt.Errorf("division by zero")
			}
			value = value.Div(right)
		} else {
			return value, nil
		}
	}
}

func (p *formulaParser) parseFactor() (decimal.Decimal, error) {
	p.skipSpaces()
	if p.consume('+') {
		return p.parseNestedFactor()
	}
	if p.consume('-') {
		value, err := p.parseNestedFactor()
		return value.Neg(), err
	}
	if p.consume('(') {
		if err := p.enter(); err != nil {
			return decimal.Zero, err
		}
		defer p.leave()
		value, err := p.parseExpression()
		if err != nil {
			return decimal.Zero, err
		}
		p.skipSpaces()
		if !p.consume(')') {
			return decimal.Zero, fmt.Errorf("missing closing parenthesis")
		}
		return value, nil
	}
	if p.pos >= len(p.input) {
		return decimal.Zero, fmt.Errorf("expected a value")
	}
	current := p.input[p.pos]
	if unicode.IsDigit(current) || current == '.' {
		return p.readNumber()
	}
	if unicode.IsLetter(current) || current == '_' {
		name, _ := p.readIdentifier()
		value, ok := p.values[name]
		if !ok {
			return decimal.Zero, fmt.Errorf("%w: %s", ErrUnknownFormulaRef, name)
		}
		return value, nil
	}
	return decimal.Zero, fmt.Errorf("unexpected character %q", current)
}

func (p *formulaParser) parseNestedFactor() (decimal.Decimal, error) {
	if err := p.enter(); err != nil {
		return decimal.Zero, err
	}
	defer p.leave()
	return p.parseFactor()
}

func (p *formulaParser) enter() error {
	if p.depth >= maxFormulaDepth {
		return fmt.Errorf("formula nesting exceeds %d levels", maxFormulaDepth)
	}
	p.depth++
	return nil
}

func (p *formulaParser) leave() {
	p.depth--
}

func (p *formulaParser) readNumber() (decimal.Decimal, error) {
	start := p.pos
	dots := 0
	for p.pos < len(p.input) && (unicode.IsDigit(p.input[p.pos]) || p.input[p.pos] == '.') {
		if p.input[p.pos] == '.' {
			dots++
		}
		p.pos++
	}
	if dots > 1 {
		return decimal.Zero, fmt.Errorf("invalid decimal number")
	}
	value, err := decimal.NewFromString(string(p.input[start:p.pos]))
	if err != nil {
		return decimal.Zero, fmt.Errorf("invalid decimal number: %w", err)
	}
	return value, nil
}

func (p *formulaParser) readIdentifier() (string, bool) {
	start := p.pos
	for p.pos < len(p.input) &&
		(unicode.IsLetter(p.input[p.pos]) || unicode.IsDigit(p.input[p.pos]) || p.input[p.pos] == '_') {
		p.pos++
	}
	if p.pos == start {
		return "", false
	}
	return strings.ToUpper(string(p.input[start:p.pos])), true
}

func (p *formulaParser) skipSpaces() {
	for p.pos < len(p.input) && unicode.IsSpace(p.input[p.pos]) {
		p.pos++
	}
}

func (p *formulaParser) consume(value rune) bool {
	if p.pos < len(p.input) && p.input[p.pos] == value {
		p.pos++
		return true
	}
	return false
}
