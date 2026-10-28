package mm

import (
	"errors"
	"net"
	"regexp"
	"strings"

	"phenix/util/plog"
)

var (
	ipv4Re              = regexp.MustCompile(`(?:\d{1,3}[.]){3}\d{1,3}(?:\/\d{1,2})?`)
	stateRe             = regexp.MustCompile(`^(?:error|quit|running|shutdown|paused)$`)
	boolOps             = regexp.MustCompile(`^(?:and|or|not)$`)
	keywordEscape       = regexp.MustCompile(`'([^']+)'`)
	defaultSearchFields = []string{"Name", "Networks", "Host", "Disk", "Tags"} //nolint:gochecknoglobals // default fields

	errMalformedFilter = errors.New("malformed search filter")
)

// The Boolean operators.
const (
	opAnd = "and"
	opOr  = "or"
	opNot = "not"
)

// Binding strength of the Boolean operators: "not" binds tightest, then
// "and", then "or".
const (
	precedenceOr = iota + 1
	precedenceAnd
	precedenceNot
)

type Stack struct {
	s []any
}

func (s *Stack) Push(item any) {
	s.s = append(s.s, item)
}

func (s *Stack) Pop() any {
	if s.IsEmpty() {
		return nil
	}

	lastItem := s.s[len(s.s)-1]
	s.s = s.s[:len(s.s)-1]

	return lastItem
}

func (s *Stack) IsEmpty() bool {
	return len(s.s) == 0
}

type ExpressionTree struct {
	left         *ExpressionTree
	right        *ExpressionTree
	term         string
	searchFields []string
}

// BuildTree parses a search filter into an expression tree. "not" binds
// tightest, then "and", then "or"; parentheses group terms, and two terms
// with no operator between them are joined with "and". BuildTree returns nil
// for an empty filter and for a filter that it cannot parse.
func BuildTree(searchFilter string) *ExpressionTree {
	tokens, err := tokenize(searchFilter)
	if err != nil {
		return nil
	}

	postFix, err := postfix(tokens)
	if err != nil {
		return nil
	}

	expressionTree, err := createTree(postFix)
	if err != nil {
		return nil
	}

	return expressionTree
}

func (node *ExpressionTree) Evaluate(vm *VM) bool {
	if node == nil {
		return false
	}

	if node.left == nil && node.right == nil {
		return node.match(vm)
	}

	rightSide := false
	if node.right != nil {
		rightSide = node.right.Evaluate(vm)
	}

	leftSide := false
	if node.left != nil {
		leftSide = node.left.Evaluate(vm)
	}

	switch node.term {
	case opAnd:
		return rightSide && leftSide

	case opOr:
		return rightSide || leftSide

	case opNot:
		return !rightSide
	}

	return false
}

// tokenize splits a lower-case copy of the filter into terms, operators and
// parentheses. A double-quoted string is one term that keeps its quotes,
// spaces and parentheses. tokenize returns errMalformedFilter for a quote
// with no end or for an empty quoted string.
func tokenize(searchFilter string) ([]string, error) {
	var tokens []string

	rest := strings.ToLower(searchFilter)

	for {
		before, after, quoted := strings.Cut(rest, `"`)
		tokens = append(tokens, splitTerms(before)...)

		if !quoted {
			return tokens, nil
		}

		term, next, closed := strings.Cut(after, `"`)
		if !closed || term == "" {
			return nil, errMalformedFilter
		}

		tokens = append(tokens, `"`+term+`"`)
		rest = next
	}
}

// splitTerms splits text with no double quotes into terms, operators and
// parentheses.
func splitTerms(text string) []string {
	text = strings.ReplaceAll(text, "(", " ( ")
	text = strings.ReplaceAll(text, ")", " ) ")

	return strings.Fields(text)
}

// literal returns the text in a term that tokenize made from a double-quoted
// string, and reports whether term is such a term.
func literal(term string) (string, bool) {
	if len(term) < 2 || term[0] != '"' || term[len(term)-1] != '"' {
		return "", false
	}

	return term[1 : len(term)-1], true
}

func precedence(operator string) int {
	switch operator {
	case opNot:
		return precedenceNot
	case opAnd:
		return precedenceAnd
	case opOr:
		return precedenceOr
	default:
		return 0
	}
}

// postfix puts the tokens into postfix order with the shunting yard
// algorithm by Edsger Dijkstra. It adds an "and" between two operands that
// have no operator between them, and returns errMalformedFilter for
// unbalanced parentheses or an operator with a missing operand.
func postfix(tokens []string) ([]string, error) {
	var output []string

	opStack := new(Stack)
	expectOperand := true

	for _, token := range tokens {
		startsOperand := token == "(" || token == opNot || !isSyntax(token)
		if !expectOperand && startsOperand {
			output = pushBinary(output, opStack, opAnd)
			expectOperand = true
		}

		switch token {
		case "(", opNot:
			opStack.Push(token)
		case ")":
			if expectOperand {
				return nil, errMalformedFilter
			}

			for {
				if opStack.IsEmpty() {
					return nil, errMalformedFilter
				}

				top, _ := opStack.Pop().(string)
				if top == "(" {
					break
				}

				output = append(output, top)
			}
		case opAnd, opOr:
			if expectOperand {
				return nil, errMalformedFilter
			}

			output = pushBinary(output, opStack, token)
			expectOperand = true
		default:
			output = append(output, token)
			expectOperand = false
		}
	}

	if expectOperand {
		return nil, errMalformedFilter
	}

	for !opStack.IsEmpty() {
		top, _ := opStack.Pop().(string)
		if top == "(" {
			return nil, errMalformedFilter
		}

		output = append(output, top)
	}

	return output, nil
}

// pushBinary moves the stacked operators that bind at least as tightly as
// operator to the output, then pushes operator. This makes "and" and "or"
// left-associative.
func pushBinary(output []string, opStack *Stack, operator string) []string {
	for !opStack.IsEmpty() {
		top, _ := opStack.Pop().(string)
		if precedence(top) < precedence(operator) {
			opStack.Push(top)

			break
		}

		output = append(output, top)
	}

	opStack.Push(operator)

	return output
}

func isSyntax(token string) bool {
	return token == "(" || token == ")" || boolOps.MatchString(token)
}

// createTree builds the expression tree from tokens in postfix order. It
// returns errMalformedFilter when an operator has no operand or operands
// are left over.
func createTree(postFix []string) (*ExpressionTree, error) {
	stack := new(Stack)

	for _, term := range postFix {
		node := new(ExpressionTree)

		switch term {
		case opNot:
			node.term = term
			node.right, _ = stack.Pop().(*ExpressionTree)

			if node.right == nil {
				return nil, errMalformedFilter
			}
		case opAnd, opOr:
			node.term = term
			node.right, _ = stack.Pop().(*ExpressionTree)
			node.left, _ = stack.Pop().(*ExpressionTree)

			if node.left == nil || node.right == nil {
				return nil, errMalformedFilter
			}
		default:
			text, isLiteral := literal(term)

			switch {
			case isLiteral:
				node.term = text
				node.searchFields = defaultSearchFields
			case keywordEscape.MatchString(term):
				node.term = keywordEscape.FindAllStringSubmatch(term, -1)[0][1]
				node.searchFields = defaultSearchFields
			default:
				node.term = term
				node.searchFields = getSearchFields(term)
			}
		}

		stack.Push(node)
	}

	expressionTree, _ := stack.Pop().(*ExpressionTree)
	if expressionTree == nil || !stack.IsEmpty() {
		return nil, errMalformedFilter
	}

	return expressionTree, nil
}

func getSearchFields(term string) []string {
	switch {
	case ipv4Re.MatchString(term):
		return []string{"IPv4"}
	case stateRe.MatchString(term):
		return []string{"State"}
	case strings.Contains(term, "capturing"):
		return []string{"Captures"}
	case strings.Contains(term, "busy"):
		return []string{"Busy"}
	case strings.Contains(term, "dnb"):
		return []string{"DoNotBoot"}
	default:
		return defaultSearchFields
	}
}

func (node *ExpressionTree) match(vm *VM) bool { //nolint:funlen // complex logic
	for _, field := range node.searchFields {
		switch field {
		case "IPv4":
			{
				cidr := node.term
				if !strings.Contains(cidr, "/") {
					cidr += "/32"
				}

				_, refNet, err := net.ParseCIDR(cidr)
				if err != nil {
					plog.Debug(plog.TypeSystem, "unable to parse network", "network", node.term)

					continue
				}

				for _, network := range vm.IPv4 {
					address := net.ParseIP(network)

					if address == nil {
						plog.Debug(plog.TypeSystem, "unable to parse address", "address", network)

						continue
					}

					match := refNet.Contains(address)
					if match {
						return match
					}
				}
			}
		case "State":
			{
				if node.term == "shutdown" || node.term == "quit" {
					return strings.ToLower(vm.State) == "quit"
				} else {
					return strings.ToLower(vm.State) == node.term
				}
			}
		case "Busy":
			{
				return vm.Busy
			}
		case "Captures":
			{
				return len(vm.Captures) > 0
			}
		case "DoNotBoot":
			{
				return vm.DoNotBoot
			}
		case "Networks":
			{
				for _, tap := range vm.Networks {
					match := strings.Contains(strings.ToLower(tap), node.term)
					if match {
						return match
					}
				}

				continue
			}
		case "Name":
			{
				match := strings.Contains(strings.ToLower(vm.Name), node.term)
				if match {
					return match
				}

				continue
			}
		case "Host":
			{
				match := strings.Contains(strings.ToLower(vm.Host), node.term)
				if match {
					return match
				}

				continue
			}
		case "Tags":
			{
				for _, tag := range vm.Tags {
					match := strings.Contains(strings.ToLower(tag), node.term)
					if match {
						return match
					}
				}

				continue
			}
		case "Disk":
			{
				match := strings.Contains(strings.ToLower(vm.Disk), node.term)
				if match {
					return match
				}

				continue
			}
		}
	}

	return false
}
