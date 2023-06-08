package file

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	dateRe              = regexp.MustCompile(`[<>=]{1,2}[ ]?\d{4}[-]\d{2}(?:[\d-]+(?:[ ][\d:]+)?)?`)
	sizeRe              = regexp.MustCompile(`[<>=]{1,2}[ ]?\d+(?:[ ]?(?:b|kb|mb|gb))?`)
	categoryRe          = regexp.MustCompile(`^(?:packet|elf|vm)`)
	comparisonOps       = regexp.MustCompile(`[<>=]{1,2}`)
	fileSizeSpec        = regexp.MustCompile(`(?:b|kb|mb|gb)`)
	boolOps             = regexp.MustCompile(`^(?:and|or|not)$`)
	groups              = regexp.MustCompile(`(?:[(][^ ])|(?:[^ ][)])`)
	keywordEscape       = regexp.MustCompile(`['"]([^'"]+)['"]`)
	defaultSearchFields = []string{"Name", "Category"}
	spaceReplacement    = "-sp-32-sp-"
)

type Stack struct {
	s []interface{}
}

func (s *Stack) Push(item interface{}) {
	s.s = append(s.s, item)

}

func (s *Stack) Pop() interface{} {
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

	node.left.PrintTree()
	node.right.PrintTree()

}

func BuildTree(searchFilter string) *ExpressionTree {
	if len(searchFilter) == 0 {
		return nil
	}

	searchFilter = strings.Trim(searchFilter, " ")

	// Adjust any parentheses so that they are
	// space delimited
	if groups.MatchString(searchFilter) {
		searchFilter = strings.ReplaceAll(searchFilter, "(", "( ")
		searchFilter = strings.ReplaceAll(searchFilter, ")", " )")
	}

	searchString := strings.ToLower(searchFilter)

	// Add any placeholder spaces

	// The date string will be a special case as we
	// replace the 1st space and the 2nd space with
	// different placeholders
	if dateRe.MatchString(searchString) {
		matches := dateRe.FindAllStringSubmatch(searchString, -1)

		for _, match := range matches {

			var replacement string
			if strings.Count(match[0], " ") > 1 {
				replacement = strings.Replace(match[0], " ", spaceReplacement, 1)
				replacement = strings.ReplaceAll(replacement, " ", "_")
			} else if strings.Count(match[0], " ") == 1 && strings.Count(match[0], "-") == 2 {
				replacement = strings.ReplaceAll(match[0], " ", "_")
			} else {
				replacement = strings.Replace(match[0], " ", spaceReplacement, 1)
			}

			searchString = strings.ReplaceAll(searchString, match[0], replacement)

		}

	}

	searchString = addPlaceholderSpaces(searchString, sizeRe)
	searchString = addPlaceholderSpaces(searchString, categoryRe)

	stringParts := strings.Split(searchString, " ")

	// If no operators were found, assume a default
	// operator of "and"
	match := false
	for _, part := range stringParts {
		if boolOps.MatchString(part) {
			match = true
			break
		}

	}

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
// postfix notation
func postfix(terms []string) ([]string, error) {

	var output []string
	opStack := new(Stack)

	for _, term := range terms {

		if len(term) == 0 {
			continue
		}

		if boolOps.MatchString(term) || term == "(" {
			opStack.Push(term)

		} else if term == ")" {
			token := ""
			for token != "(" {
				if tmpToken, ok := opStack.Pop().(string); !ok {
					return output, fmt.Errorf("type assertion parsing token")
				} else {
					token = tmpToken
				}

				if token != "(" {
					output = append(output, token)
				}

			}

		} else {

			output = append(output, term)

		}

	}

	for !opStack.IsEmpty() {

		if token, ok := opStack.Pop().(string); !ok {
			return output, fmt.Errorf("type assertion parsing token")
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
				return nil, fmt.Errorf("type assertion parsing token")
			} else {
				opTree.right = t1
			}

			if !stack.IsEmpty() && term != "not" {

				if t2, ok := stack.Pop().(*ExpressionTree); !ok {
					return nil, fmt.Errorf("type assertion parsing token")
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

				// Replace any space placeholders to return
				// the correct search fields
				operand.term = strings.ReplaceAll(operand.term, spaceReplacement, "")

				operand.searchFields = getSearchFields(operand.term)

			}

			stack.Push(operand)

		}

	}

	if expressionTree, ok := stack.Pop().(*ExpressionTree); !ok {
		return nil, fmt.Errorf("type assertion parsing token")
	} else {
		return expressionTree, nil
	}

}

func getSearchFields(term string) []string {

	if dateRe.MatchString(term) {
		return []string{"Date"}

	} else if sizeRe.MatchString(term) {
		return []string{"Size"}

	} else if categoryRe.MatchString(term) {
		return []string{"Category"}

	} else {
		return defaultSearchFields

	}

}

func (node *ExpressionTree) match(file *File) bool {

	for _, field := range node.searchFields {
		switch field {
		case "Date":
			{
				var (
					compOp  string
					newTerm string
					layout  string
				)

				// Try to determine the date format
				switch numHyphens := strings.Count(node.term, "-"); numHyphens {
				case 1:
					layout = "2006-01"

				case 2:
					switch numColons := strings.Count(node.term, ":"); numColons {
					case 0:
						layout = "2006-01-02"
						if strings.Contains(node.term, "_") {
							layout = "2006-01-02_15"
						}
					case 1:
						layout = "2006-01-02_15:04"
					case 2:
						layout = "2006-01-02_15:04:05"
					}

				}

				if comparisonOps.MatchString(node.term) {
					compOp = comparisonOps.FindAllStringSubmatch(node.term, -1)[0][0]
					newTerm = comparisonOps.ReplaceAllString(node.term, "")
				}

				// Make sure a valid comparison operator was found
				if len(compOp) == 0 {
					return false
				}

				t, err := time.Parse(layout, newTerm)
				if err != nil {
					return false
				}

				switch compOp {
				case "<":
					return file.dateTime.Before(t)
				case ">":
					return file.dateTime.After(t)
				case "=":
					return dateTimeEqual(file.dateTime, t, layout)
				case ">=":
					return file.dateTime.After(t) || dateTimeEqual(file.dateTime, t, layout)
				case "<=":
					return file.dateTime.Before(t) || dateTimeEqual(file.dateTime, t, layout)

				}

			}
		case "Size":
			{
				var (
					compOp   string
					newTerm  string
					fileSize interface{}
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
						fileSize = fileSize.(int) * int(math.Pow10(3))
					case "mb":
						fileSize = fileSize.(int) * int(math.Pow10(6))
					case "gb":
						fileSize = fileSize.(int) * int(math.Pow10(9))
					}

				}

				// Check if fileSize has already been converted to an int
				if _, ok := fileSize.(int); !ok {

					fileSize, err = strconv.Atoi(newTerm)
					if err != nil {
						return false
					}

				}

				switch compOp {
				case "<":
					return file.Size < fileSize.(int64)
				case ">":
					return file.Size > fileSize.(int64)
				case "=":
					return file.Size == fileSize.(int64)
				case ">=":
					return file.Size > fileSize.(int64) || file.Size == fileSize.(int64)
				case "<=":
					return file.Size < fileSize.(int64) || file.Size == fileSize.(int64)

				}

			}
			/*
				case "Category":
					{
						match := strings.Contains(strings.ToLower(file.Category), node.term)
						if match {
							return match
						}

						continue
					}
			*/
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

func dateTimeEqual(t, t1 time.Time, layout string) bool {

	switch layout {
	case "2006-01":
		return t.Year() == t1.Year() && t.Month() == t1.Month()
	case "2006-01-02":
		return t.Year() == t1.Year() && t.Month() == t1.Month() && t.Day() == t1.Day()
	case "2006-01-02_15":
		yearMonthDay := t.Year() == t1.Year() && t.Month() == t1.Month() && t.Day() == t1.Day()
		return yearMonthDay && t.Hour() == t1.Hour()
	case "2006-01-02_15:04":
		yearMonthDay := t.Year() == t1.Year() && t.Month() == t1.Month() && t.Day() == t1.Day()
		return yearMonthDay && t.Hour() == t1.Hour() && t.Minute() == t1.Minute()
	case "2006-01-02_15:04:05":
		yearMonthDay := t.Year() == t1.Year() && t.Month() == t1.Month() && t.Day() == t1.Day()
		return yearMonthDay && t.Hour() == t1.Hour() && t.Minute() == t1.Minute() && t.Second() == t1.Second()
	}

	return false

}

func addPlaceholderSpaces(searchString string, pattern *regexp.Regexp) string {

	// Replace spaces with the replacement string
	if pattern.MatchString(searchString) {
		extracted := pattern.FindAllStringSubmatch(searchString, -1)
		for _, match := range extracted {
			replacement := strings.ReplaceAll(match[0], " ", spaceReplacement)
			searchString = strings.ReplaceAll(searchString, match[0], replacement)
		}

	}

	return searchString
}
