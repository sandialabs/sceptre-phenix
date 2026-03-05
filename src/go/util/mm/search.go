package mm

import (
	"errors"
	"net"
	"regexp"
	"slices"
	"strings"

	"phenix/util/plog"
)

var (
	ipv4Re              = regexp.MustCompile(`(?:\d{1,3}[.]){3}\d{1,3}(?:\/\d{1,2})?`)
	stateRe             = regexp.MustCompile(`^(?:error|quit|running|shutdown|paused)$`)
	boolOps             = regexp.MustCompile(`^(?:and|or|not)$`)
	groups              = regexp.MustCompile(`(?:[(][^ ])|(?:[^ ][)])`)
	keywordEscape       = regexp.MustCompile(`['"]([^'"]+)['"]`)
	defaultSearchFields = []string{"Name", "Networks", "Host", "Disk", "Tags"} //nolint:gochecknoglobals // default fields
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

func (node *ExpressionTree) PrintTree() {
	if node == nil {
		return
	}

	// fmt.Printf("Node:%s Fields:%v\n",node.term,node.searchFields)

	node.left.PrintTree()
	node.right.PrintTree()
}

func BuildTree(searchFilter string) *ExpressionTree {
	if len(searchFilter) == 0 {
		return nil
	}

	// Adjust any parentheses so that they are
	// space delimited
	if groups.MatchString(searchFilter) {
		searchFilter = strings.ReplaceAll(searchFilter, "(", "( ")
		searchFilter = strings.ReplaceAll(searchFilter, ")", " )")
	}

	searchString := strings.ToLower(searchFilter)
	stringParts := strings.Split(searchString, " ")

	// If no operators were found, assume a default
	// operator of "and"
	match := slices.ContainsFunc(stringParts, boolOps.MatchString)

	if !match {
		tmp := strings.Join(stringParts, " and ")
		stringParts = strings.Split(tmp, " ")
	}

	postFix, err := postfix(stringParts)
	if err != nil {
		return nil
	}

	// If the only term that was passed in
	// is a boolean operator, then skip
	// building the tree
	if len(postFix) == 1 {
		if boolOps.MatchString(postFix[0]) {
			return nil
		}
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
	case "and":
		return rightSide && leftSide

	case "or":
		return rightSide || leftSide

	case "not":
		return !rightSide
	}

	return false
}

// Shunting yard algorithm by Edsger Dijkstra
// for putting search terms and operators into
// postfix notation.
func postfix(terms []string) ([]string, error) {
	var output []string

	opStack := new(Stack)

	for _, term := range terms {
		if len(term) == 0 {
			continue
		}

		switch {
		case boolOps.MatchString(term) || term == "(":
			opStack.Push(term)
		case term == ")":
			token := ""
			for token != "(" {
				if tmpToken, ok := opStack.Pop().(string); !ok {
					return output, errors.New("error: type assertion parsing token")
				} else {
					token = tmpToken
				}

				if token != "(" {
					output = append(output, token)
				}
			}
		default:
			output = append(output, term)
		}
	}

	for !opStack.IsEmpty() {
		if token, ok := opStack.Pop().(string); !ok {
			return output, errors.New("error: type assertion parsing token")
		} else {
			output = append(output, token)
		}
	}

	return output, nil
}

func createTree(postFix []string) (*ExpressionTree, error) {
	stack := new(Stack)

	for _, term := range postFix {
		if boolOps.MatchString(term) {
			opTree := new(ExpressionTree)
			opTree.term = term

			if t1, ok := stack.Pop().(*ExpressionTree); !ok {
				return nil, errors.New("error: type assertion parsing token")
			} else {
				opTree.right = t1
			}

			if !stack.IsEmpty() && term != "not" {
				if t2, ok := stack.Pop().(*ExpressionTree); !ok {
					return nil, errors.New("error: type assertion parsing token")
				} else {
					opTree.left = t2
				}
			}

			stack.Push(opTree)
		} else {
			operand := new(ExpressionTree)
			if keywordEscape.MatchString(term) {
				operand.term = keywordEscape.FindAllStringSubmatch(term, -1)[0][1]
				operand.searchFields = defaultSearchFields
			} else {
				operand.term = term
				operand.searchFields = getSearchFields(term)
			}

			stack.Push(operand)
		}
	}

	if expressionTree, ok := stack.Pop().(*ExpressionTree); !ok {
		return nil, errors.New("error: type assertion parsing token")
	} else {
		return expressionTree, nil
	}
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
				_, refNet, err := net.ParseCIDR(node.term)
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
