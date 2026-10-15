package calc

import (
	"fmt"
	"math/big"
)

// Binding powers of the Pratt parser.
const (
	bpAdd     = 10 // + -
	bpMul     = 20 // * / % and implicit multiplication
	bpUnary   = 25 // prefix - +; looser than ^ so that -2^2 = -4
	bpPow     = 30 // ^, right-associative
	bpPostfix = 40 // !
)

// maxDepth bounds the nesting of sub-expressions.
const maxDepth = 100

// parser is a Pratt parser that evaluates while parsing.
type parser struct {
	toks  []token
	pos   int
	depth int
	// ops counts the operations applied (binary and postfix operators,
	// function calls and implicit multiplications). Unary signs and
	// parentheses do not count.
	ops int
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tokEOF {
		p.pos++
	}
	return t
}

// parse evaluates the whole token stream.
func (p *parser) parse() (*big.Float, error) {
	v, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEOF {
		return nil, fmt.Errorf("%w: unexpected %q", errSyntax, t.text)
	}
	return v, nil
}

// infixBP returns the left binding power of t used as an infix or postfix
// operator, or 0 if t ends an expression.
func infixBP(t token) int {
	switch t.kind {
	case tokOp:
		switch t.text {
		case "+", "-":
			return bpAdd
		case "*", "/", "%":
			return bpMul
		case "^":
			return bpPow
		case "!":
			return bpPostfix
		}
	case tokLParen, tokIdent:
		return bpMul // implicit multiplication: 2pi, 3(4+5)
	}
	return 0
}

// expr parses an expression whose operators bind tighter than rbp.
func (p *parser) expr(rbp int) (*big.Float, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return nil, fmt.Errorf("%w: expression too deep", errSyntax)
	}
	left, err := p.prefix()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		bp := infixBP(t)
		if bp <= rbp {
			return left, nil
		}
		p.ops++
		switch {
		case t.text == "!":
			p.next()
			left, err = factorial(left)
		case t.kind != tokOp:
			var right *big.Float
			if right, err = p.expr(bpMul); err == nil {
				left, err = binary("*", left, right)
			}
		default:
			p.next()
			next := bp
			if t.text == "^" {
				next-- // right-associative
			}
			var right *big.Float
			if right, err = p.expr(next); err == nil {
				left, err = binary(t.text, left, right)
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

// prefix parses an operand: a number, constant, function call,
// parenthesized expression or signed operand.
func (p *parser) prefix() (*big.Float, error) {
	t := p.next()
	switch t.kind {
	case tokNum:
		return newF().Set(t.num), nil
	case tokIdent:
		if f, ok := functions[t.text]; ok {
			if p.next().kind != tokLParen {
				return nil, fmt.Errorf("%w: %s needs parentheses", errSyntax, t.text)
			}
			p.ops++
			args, err := p.args()
			if err != nil {
				return nil, err
			}
			return f.apply(t.text, args)
		}
		if c, ok := constants[t.text]; ok {
			return newF().Set(c), nil
		}
		return nil, fmt.Errorf("%w %q", errUnknown, t.text)
	case tokLParen:
		v, err := p.expr(0)
		if err != nil {
			return nil, err
		}
		if p.next().kind != tokRParen {
			return nil, fmt.Errorf("%w: missing )", errSyntax)
		}
		return v, nil
	case tokOp:
		if t.text == "-" || t.text == "+" {
			v, err := p.expr(bpUnary)
			if err != nil {
				return nil, err
			}
			if t.text == "-" {
				v.Neg(v)
			}
			return v, nil
		}
	}
	return nil, fmt.Errorf("%w: unexpected %q", errSyntax, t.text)
}

// args parses a function's argument list after its "(".
func (p *parser) args() ([]*big.Float, error) {
	var args []*big.Float
	if p.peek().kind == tokRParen {
		p.next()
		return args, nil
	}
	for {
		v, err := p.expr(0)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
		switch p.next().kind {
		case tokComma:
		case tokRParen:
			return args, nil
		default:
			return nil, fmt.Errorf("%w: missing )", errSyntax)
		}
	}
}
