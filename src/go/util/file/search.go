package file

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	dateFormatPartsYearMonth = 1
	dateFormatPartsFull      = 2
	dateFormatPartsTime      = 1
	dateFormatPartsTimeSec   = 2
	fileSizeKB               = 3
	fileSizeMB               = 6
	fileSizeGB               = 9
)

var (
	dateRe = regexp.MustCompile(
		`[<>=]{1,2}[ ]?\d{4}[-]\d{2}(?:[\d-]+(?:[ ][\d:]+)?)?`,
	)
	sizeRe = regexp.MustCompile(
		`[<>=]{1,2}[ ]?\d+(?:[ ]?(?:b|kb|mb|gb))?\b`,
	)
	categoryRe = regexp.MustCompile(
		`^(?:packet|elf|vm)`,
	)
	comparisonOps = regexp.MustCompile(
		`[<>=]{1,2}`,
	)
	fileSizeSpec = regexp.MustCompile(
		`(?:b|kb|mb|gb)`,
	)
	boolOps = regexp.MustCompile(
		`^(?:and|or|not)$`,
	)
	keywordEscape = regexp.MustCompile(
		`'([^']+)'`,
	)
	defaultSearchFields = []string{ //nolint:gochecknoglobals // default fields
		"Name",
	}
	categorySearchFields = []string{ //nolint:gochecknoglobals // category fields
		"Name",
		"Category",
	}

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

func (node *ExpressionTree) Evaluate(experimentFile *File) bool {
	if node == nil {
		return false
	}

	if node.left == nil && node.right == nil {
		return node.match(experimentFile)
	}

	rightSide := false
	if node.right != nil {
		rightSide = node.right.Evaluate(experimentFile)
	}

	leftSide := false
	if node.left != nil {
		leftSide = node.left.Evaluate(experimentFile)
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
// parentheses. It first removes the spaces in date and size terms, and
// writes the space between a date and a time as "_".
func splitTerms(text string) []string {
	text = dateRe.ReplaceAllStringFunc(text, func(term string) string {
		operator := comparisonOps.FindString(term)
		date := strings.TrimPrefix(term[len(operator):], " ")

		return operator + strings.Replace(date, " ", "_", 1)
	})
	text = sizeRe.ReplaceAllStringFunc(text, func(term string) string {
		return strings.ReplaceAll(term, " ", "")
	})
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
	if startsWith(dateRe, term) {
		return []string{"Date"}
	}
	if startsWith(sizeRe, term) {
		return []string{"Size"}
	}
	if categoryRe.MatchString(term) {
		return categorySearchFields
	}
	return defaultSearchFields
}

// startsWith reports whether re matches at the start of term, so a name such
// as name>5 is not read as a size or a date.
func startsWith(re *regexp.Regexp, term string) bool {
	loc := re.FindStringIndex(term)

	return loc != nil && loc[0] == 0
}

//nolint:funlen // complex logic
func (node *ExpressionTree) match(file *File) bool {
	for _, field := range node.searchFields {
		switch field {
		case "Date":
			return matchDate(node.term, file.dateTime)
		case "Size":
			{
				var (
					compOp   string
					newTerm  string
					fileSize any
					err      error
				)

				if comparisonOps.MatchString(node.term) {
					compOp = comparisonOps.FindAllStringSubmatch(node.term, -1)[0][0]
					newTerm = comparisonOps.ReplaceAllString(node.term, "")
				}

				// Make sure a valid comparison operator was found
				if len(compOp) == 0 {
					return false
				}

				if fileSizeSpec.MatchString(node.term) {
					spec := fileSizeSpec.FindAllStringSubmatch(newTerm, -1)[0][0]
					newTerm = fileSizeSpec.ReplaceAllString(newTerm, "")

					fileSize, err = strconv.ParseInt(newTerm, 10, 64)
					if err != nil {
						return false
					}

					switch spec {
					case "kb":
						fs, _ := fileSize.(int64)
						fileSize = fs * int64(math.Pow10(fileSizeKB))
					case "mb":
						fs, _ := fileSize.(int64)
						fileSize = fs * int64(math.Pow10(fileSizeMB))
					case "gb":
						fs, _ := fileSize.(int64)
						fileSize = fs * int64(math.Pow10(fileSizeGB))
					}
				}

				// Check if fileSize has already been converted to an int
				if _, ok := fileSize.(int64); !ok {
					fs, err := strconv.Atoi(newTerm)
					if err != nil {
						return false
					}
					fileSize = int64(fs)
				}

				switch compOp {
				case "<":
					fs, _ := fileSize.(int64)
					return file.Size < fs
				case ">":
					fs, _ := fileSize.(int64)
					return file.Size > fs
				case "=":
					fs, _ := fileSize.(int64)
					return file.Size == fs
				case ">=":
					fs, _ := fileSize.(int64)
					return file.Size > fs || file.Size == fs
				case "<=":
					fs, _ := fileSize.(int64)
					return file.Size < fs || file.Size == fs
				}
			}
		case "Category":
			for _, category := range file.Categories {
				if strings.Contains(strings.ToLower(category), node.term) {
					return true
				}
			}

			continue
		case "Name":
			{
				match := strings.Contains(strings.ToLower(file.Name), node.term)
				if match {
					return match
				}

				continue
			}
		}
	}

	return false
}

// matchDate reports whether the time at is in the relation that term names
// with the period of term. The period is a month, day, hour, minute or second
// in the time zone of at. "<" is before the period, "<=" is before its end,
// "=" is in it, ">=" is at or after its start and ">" is after its end.
func matchDate(term string, at time.Time) bool {
	var layout string

	switch strings.Count(term, "-") {
	case dateFormatPartsYearMonth:
		layout = "2006-01"
	case dateFormatPartsFull:
		switch strings.Count(term, ":") {
		case 0:
			layout = "2006-01-02"
			if strings.Contains(term, "_") {
				layout = "2006-01-02_15"
			}
		case dateFormatPartsTime:
			layout = "2006-01-02_15:04"
		case dateFormatPartsTimeSec:
			layout = "2006-01-02_15:04:05"
		}
	}

	compOp := comparisonOps.FindString(term)

	start, err := time.ParseInLocation(layout, comparisonOps.ReplaceAllString(term, ""), at.Location())
	if layout == "" || err != nil {
		return false
	}

	end := periodEnd(start, layout)

	switch compOp {
	case "<":
		return at.Before(start)
	case "<=":
		return at.Before(end)
	case "=":
		return !at.Before(start) && at.Before(end)
	case ">=":
		return !at.Before(start)
	case ">":
		return !at.Before(end)
	}

	return false
}

// periodEnd returns the start of the period that follows the period that
// starts at start.
func periodEnd(start time.Time, layout string) time.Time {
	switch layout {
	case "2006-01":
		return start.AddDate(0, 1, 0)
	case "2006-01-02":
		return start.AddDate(0, 0, 1)
	case "2006-01-02_15":
		return start.Add(time.Hour)
	case "2006-01-02_15:04":
		return start.Add(time.Minute)
	default:
		return start.Add(time.Second)
	}
}
