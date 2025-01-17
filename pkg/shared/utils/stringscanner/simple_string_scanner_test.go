package stringscanner

import (
	"testing"

	. "github.com/onsi/gomega"
)

func Test_SimpleScanner(t *testing.T) {
	tests := []struct {
		name           string
		value          string
		expectedTokens []Token
	}{
		{
			name:           "Testing empty string",
			value:          "",
			expectedTokens: []Token{},
		},
		{
			name:  "Testing 1 token",
			value: "a",
			expectedTokens: []Token{
				{TokenType: ALPHA, Value: "a", Position: 0},
			},
		},
		{
			name:  "Testing 5 tokens",
			value: "ab(1.",
			expectedTokens: []Token{
				{TokenType: ALPHA, Value: "a", Position: 0},
				{TokenType: ALPHA, Value: "b", Position: 1},
				{TokenType: SYMBOL, Value: "(", Position: 2},
				{TokenType: DIGIT, Value: "1", Position: 3},
				{TokenType: DECIMALPOINT, Value: ".", Position: 4},
			},
		},
	}

	RegisterTestingT(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := NewSimpleScanner()
			scanner.Init(tt.value)
			allTokens := []Token{}
			for scanner.Next() {
				allTokens = append(allTokens, *scanner.Token())
			}
			Expect(allTokens).To(Equal(tt.expectedTokens))
		})
	}
}

func Test_simpleStringScanner_Peek(t *testing.T) {
	tests := []struct {
		name    string
		s       *simpleStringScanner
		want    bool
		wantVal *Token
	}{
		{
			name: "return true and update token if pos < length of value -1",
			s: &simpleStringScanner{
				pos:   1,
				value: "testValue",
			},
			want: true,
			wantVal: &Token{
				TokenType: 0,
				Value:     "s",
				Position:  2,
			},
		},
		{
			name: "return false and nil if pos > length of value -1",
			s: &simpleStringScanner{

				pos:   10,
				value: "testValue1",
			},
			want:    false,
			wantVal: nil,
		},
		{
			name: "return false and nil if pos == length of value -1",
			s: &simpleStringScanner{

				pos:   9,
				value: "testValue2",
			},
			want:    false,
			wantVal: nil,
		},
	}

	RegisterTestingT(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotVal := tt.s.Peek()
			Expect(got).To(Equal(tt.want))
			Expect(gotVal).To(Equal(tt.wantVal))
		})
	}
}
