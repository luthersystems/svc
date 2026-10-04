// Package parser provides a handlebars syntax analyser. It consumes the tokens provided by the lexer to build an AST.
package parser

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/luthersystems/svc/libhandlebars/hbs/internal/ast"
	"github.com/luthersystems/svc/libhandlebars/hbs/internal/lexer"
)

// References:
//   - https://github.com/wycats/handlebars.js/blob/master/src/handlebars.yy
//   - https://github.com/golang/go/blob/master/src/text/template/parse/parse.go

// parser is a syntax analyzer.
type parser struct {
	// Lexer
	lex *lexer.Lexer

	// Tokens are allocated from slabs: one allocation per tokenSlab
	// tokens. A slab is never reused, so token pointers stay valid.
	slab []lexer.Token

	// Tokens parsed but not consumed yet; tokens[head:] are live
	tokens []*lexer.Token
	head   int

	// Nesting depth of the construct being parsed, and its limit (0: none)
	depth    int
	maxDepth int

	// All tokens have been retreieved from lexer
	lexOver bool
}

// tokenSlab is the number of tokens allocated at once.
const tokenSlab = 64

// LimitError reports a template nested deeper than the limit. The
// prescan (Depth) reports it before parsing; the parser's own depth
// counter reports it too, as a second line of defence.
type LimitError struct {
	Msg string
}

func (e *LimitError) Error() string { return e.Msg }

// DepthError returns the error for nesting deeper than maxDepth, reached
// at the given line. Depth and the parser use the same message.
func DepthError(maxDepth, line int) *LimitError {
	return &LimitError{Msg: fmt.Sprintf("Parse error on line %d:\ntemplate nesting depth exceeds limit of %d", line, maxDepth)}
}

// Parse analyzes given input and returns the AST root node.
func Parse(input string) (*ast.Program, error) {
	return ParseLimit(input, 0)
}

// ParseLimit is Parse with a nesting limit: a template whose blocks
// (counting each else-if link), raw blocks and subexpressions nest deeper
// than maxDepth fails with a *LimitError. maxDepth <= 0 means no limit.
//
// The parser recurses once per nesting level, so callers taking untrusted
// input must check Depth first and pass the same limit here.
func ParseLimit(input string, maxDepth int) (*ast.Program, error) {
	var result *ast.Program
	var err error

	func() {
		// recover error
		defer errRecover(&err)

		p := &parser{
			lex:      lexer.Scan(input),
			maxDepth: maxDepth,
		}

		// parse
		result = p.parseProgram()

		// check last token
		token := p.shift()
		if token.Kind != lexer.TokenEOF {
			// Parsing ended before EOF
			errToken(token, "Syntax error")
		}

		// fix whitespaces
		processWhitespaces(result)
	}()

	if err != nil {
		return nil, err
	}
	return result, nil
}

// Depth scans input's tokens without recursion, in time linear in its
// length, and reports a *LimitError at the first token that takes the
// nesting depth (as ParseLimit counts it) past maxDepth. It stops at the
// first lexer error, where the parser stops too. It also returns the number
// of tokens scanned, which bounds the size of the AST the parser builds.
func Depth(input string, maxDepth int) (int, error) {
	return DepthFunc(input, maxDepth, nil)
}

// DepthFunc is Depth calling onNumber, if not nil, with each number token's
// text, so the caller can price the literals the parser will convert.
func DepthFunc(input string, maxDepth int, onNumber func(string)) (int, error) {
	type frame struct{ links int }
	var blocks []frame
	depth, sexprs, raw := 0, 0, false

	l := lexer.Scan(input)
	tokens := 0
	for {
		tok := l.NextToken()
		tokens++
		if onNumber != nil && tok.Kind == lexer.TokenNumber {
			onNumber(tok.Val)
		}
		switch tok.Kind {
		case lexer.TokenEOF, lexer.TokenError:
			return tokens, nil
		case lexer.TokenOpenBlock, lexer.TokenOpenInverse:
			blocks = append(blocks, frame{})
			depth++
		case lexer.TokenOpenInverseChain:
			if len(blocks) > 0 {
				blocks[len(blocks)-1].links++
			}
			depth++
		case lexer.TokenOpenEndBlock:
			if len(blocks) > 0 {
				depth -= 1 + blocks[len(blocks)-1].links
				blocks = blocks[:len(blocks)-1]
			}
			continue
		case lexer.TokenOpenRawBlock:
			raw = true
			depth++
		case lexer.TokenOpenEndRawBlock:
			if raw {
				raw = false
				depth--
			}
			continue
		case lexer.TokenOpenSexpr:
			sexprs++
			depth++
		case lexer.TokenCloseSexpr:
			if sexprs > 0 {
				sexprs--
				depth--
			}
			continue
		default:
			continue
		}
		if maxDepth > 0 && depth > maxDepth {
			return tokens, DepthError(maxDepth, tok.Line)
		}
	}
}

// enter records one more level of nesting, opened by tok.
func (p *parser) enter(tok *lexer.Token) {
	p.depth++
	if p.maxDepth > 0 && p.depth > p.maxDepth {
		panic(DepthError(p.maxDepth, tok.Line))
	}
}

// leave closes the level opened by the matching enter.
func (p *parser) leave() {
	p.depth--
}

// errRecover recovers parsing panic
func errRecover(errp *error) {
	e := recover()
	if e == nil {
		return
	}
	var rerr runtime.Error
	err, ok := e.(error)
	if !ok || errors.As(err, &rerr) {
		panic(e)
	}
	*errp = err
}

// errPanic panics
func errPanic(err error, line int) {
	panic(fmt.Errorf("Parse error on line %d:\n%w", line, err))
}

// errNode panics with given node infos
func errNode(node ast.Node, msg string) {
	errPanic(fmt.Errorf("%s\nNode: %s", msg, node), node.Location().Line)
}

// errNode panics with given Token infos
func errToken(tok *lexer.Token, msg string) {
	errPanic(fmt.Errorf("%s\nToken: %s", msg, tok), tok.Line)
}

// errNode panics because of an unexpected Token kind
func errExpected(expect lexer.TokenKind, tok *lexer.Token) {
	// raymond's text, capital included: error messages must not change
	msg := fmt.Sprintf("Expecting %s, got: '%s'", expect, tok)
	errPanic(errors.New(msg), tok.Line)
}

// program : statement*
func (p *parser) parseProgram() *ast.Program {
	result := ast.NewProgram(p.next().Pos, p.next().Line)

	for p.isStatement() {
		result.AddStatement(p.parseStatement())
	}

	return result
}

// statement : mustache | block | rawBlock | partial | content | COMMENT
func (p *parser) parseStatement() ast.Node {
	var result ast.Node

	tok := p.next()

	switch tok.Kind {
	case lexer.TokenOpen, lexer.TokenOpenUnescaped:
		// mustache
		result = p.parseMustache()
	case lexer.TokenOpenBlock:
		// block
		result = p.parseBlock()
	case lexer.TokenOpenInverse:
		// block
		result = p.parseInverse()
	case lexer.TokenOpenRawBlock:
		// rawBlock
		result = p.parseRawBlock()
	case lexer.TokenOpenPartial:
		// partial
		result = p.parsePartial()
	case lexer.TokenContent:
		// content
		result = p.parseContent()
	case lexer.TokenComment:
		// COMMENT
		result = p.parseComment()
	default:
		// isStatement admits no other kind
	}

	return result
}

// isStatement returns true if next token starts a statement
func (p *parser) isStatement() bool {
	if !p.have(1) {
		return false
	}

	switch p.next().Kind {
	case lexer.TokenOpen, lexer.TokenOpenUnescaped, lexer.TokenOpenBlock,
		lexer.TokenOpenInverse, lexer.TokenOpenRawBlock, lexer.TokenOpenPartial,
		lexer.TokenContent, lexer.TokenComment:
		return true
	default:
		return false
	}
}

// content : CONTENT
func (p *parser) parseContent() *ast.ContentStatement {
	// CONTENT
	tok := p.shift()
	if tok.Kind != lexer.TokenContent {
		// @todo This check can be removed if content is optional in a raw block
		errExpected(lexer.TokenContent, tok)
	}

	return ast.NewContentStatement(tok.Pos, tok.Line, tok.Val)
}

// COMMENT
func (p *parser) parseComment() *ast.CommentStatement {
	// COMMENT
	tok := p.shift()

	value := trimCommentClose(trimCommentOpen(tok.Val))

	result := ast.NewCommentStatement(tok.Pos, tok.Line, value)
	result.Strip = ast.NewStripForStr(tok.Val)

	return result
}

// trimCommentOpen removes ^\{\{~?!-?-? (raymond's rOpenComment).
func trimCommentOpen(s string) string {
	t, ok := strings.CutPrefix(s, "{{")
	if !ok {
		return s
	}
	t = strings.TrimPrefix(t, "~")
	t, ok = strings.CutPrefix(t, "!")
	if !ok {
		return s
	}
	t = strings.TrimPrefix(t, "-")
	return strings.TrimPrefix(t, "-")
}

// trimCommentClose removes -?-?~?\}\}$ (raymond's rCloseComment). The
// leftmost match is the longest suffix of that shape.
func trimCommentClose(s string) string {
	t, ok := strings.CutSuffix(s, "}}")
	if !ok {
		return s
	}
	t = strings.TrimSuffix(t, "~")
	t = strings.TrimSuffix(t, "-")
	return strings.TrimSuffix(t, "-")
}

// isOpenAmp matches ^\{\{~?& (raymond's rOpenAmp).
func isOpenAmp(s string) bool {
	t, ok := strings.CutPrefix(s, "{{")
	return ok && strings.HasPrefix(strings.TrimPrefix(t, "~"), "&")
}

// param* hash?
func (p *parser) parseExpressionParamsHash() ([]ast.Node, *ast.Hash) {
	var params []ast.Node
	var hash *ast.Hash

	// params*
	if p.isParam() {
		params = p.parseParams()
	}

	// hash?
	if p.isHashSegment() {
		hash = p.parseHash()
	}

	return params, hash
}

// helperName param* hash?
func (p *parser) parseExpression(tok *lexer.Token) *ast.Expression {
	result := ast.NewExpression(tok.Pos, tok.Line)

	// helperName
	result.Path = p.parseHelperName()

	// param* hash?
	result.Params, result.Hash = p.parseExpressionParamsHash()

	return result
}

// rawBlock : openRawBlock content endRawBlock
// openRawBlock : OPEN_RAW_BLOCK helperName param* hash? CLOSE_RAW_BLOCK
// endRawBlock : OPEN_END_RAW_BLOCK helperName CLOSE_RAW_BLOCK
func (p *parser) parseRawBlock() *ast.BlockStatement {
	// OPEN_RAW_BLOCK
	tok := p.shift()
	p.enter(tok)
	defer p.leave()

	result := ast.NewBlockStatement(tok.Pos, tok.Line)

	// helperName param* hash?
	result.Expression = p.parseExpression(tok)

	openName := result.Expression.Canonical()

	// CLOSE_RAW_BLOCK
	tok = p.shift()
	if tok.Kind != lexer.TokenCloseRawBlock {
		errExpected(lexer.TokenCloseRawBlock, tok)
	}

	// content
	// @todo Is content mandatory in a raw block ?
	content := p.parseContent()

	program := ast.NewProgram(tok.Pos, tok.Line)
	program.AddStatement(content)

	result.Program = program

	// OPEN_END_RAW_BLOCK
	tok = p.shift()
	if tok.Kind != lexer.TokenOpenEndRawBlock {
		// should never happen as it is caught by lexer
		errExpected(lexer.TokenOpenEndRawBlock, tok)
	}

	// helperName
	endID := p.parseHelperName()

	closeName, ok := ast.HelperNameStr(endID)
	if !ok {
		errNode(endID, "Erroneous closing expression")
	}

	if openName != closeName {
		errNode(endID, fmt.Sprintf("%s doesn't match %s", openName, closeName))
	}

	// CLOSE_RAW_BLOCK
	tok = p.shift()
	if tok.Kind != lexer.TokenCloseRawBlock {
		errExpected(lexer.TokenCloseRawBlock, tok)
	}

	return result
}

// block : openBlock program inverseChain? closeBlock
func (p *parser) parseBlock() *ast.BlockStatement {
	p.enter(p.next())
	defer p.leave()

	// openBlock
	result, blockParams := p.parseOpenBlock()

	// program
	program := p.parseProgram()
	program.BlockParams = blockParams
	result.Program = program

	// inverseChain?
	if p.isInverseChain() {
		result.Inverse = p.parseInverseChain()
	}

	// closeBlock
	p.parseCloseBlock(result)

	setBlockInverseStrip(result)

	return result
}

// setBlockInverseStrip records a block's whitespace control around its
// inverse, for the whitespace pass (whitespace.go), as handlebars.js's
// prepareBlock does (lib/handlebars/compiler/helper.js), and raymond with
// it. It is called when parsing `block` (openBlock | openInverse) and
// `inverseChain`:
//
//   - InverseStrip is the strip flags of the `{{else}}` (or `{{^}}`) that
//     opens the inverse: `{{~else~}}` trims around it.
//   - A chained inverse (`{{else if c}}`) is itself a block with no closing
//     tag of its own: the outer block's `{{/if}}` closes it, so it takes the
//     outer block's CloseStrip, and `{{~/if}}` trims the end of its program.
func setBlockInverseStrip(block *ast.BlockStatement) {
	if block.Inverse == nil {
		return
	}

	if block.Inverse.Chained {
		b, _ := block.Inverse.Body[0].(*ast.BlockStatement)
		b.CloseStrip = block.CloseStrip
	}

	block.InverseStrip = block.Inverse.Strip
}

// block : openInverse program inverseAndProgram? closeBlock
func (p *parser) parseInverse() *ast.BlockStatement {
	p.enter(p.next())
	defer p.leave()

	// openInverse
	result, blockParams := p.parseOpenBlock()

	// program
	program := p.parseProgram()

	program.BlockParams = blockParams
	result.Inverse = program

	// inverseAndProgram?
	if p.isInverse() {
		result.Program = p.parseInverseAndProgram()
	}

	// closeBlock
	p.parseCloseBlock(result)

	setBlockInverseStrip(result)

	return result
}

// helperName param* hash? blockParams?
func (p *parser) parseOpenBlockExpression(tok *lexer.Token) (*ast.BlockStatement, []string) {
	var blockParams []string

	result := ast.NewBlockStatement(tok.Pos, tok.Line)

	// helperName param* hash?
	result.Expression = p.parseExpression(tok)

	// blockParams?
	if p.isBlockParams() {
		blockParams = p.parseBlockParams()
	}

	// named returned values
	return result, blockParams
}

// inverseChain : openInverseChain program inverseChain?
//
//	| inverseAndProgram
func (p *parser) parseInverseChain() *ast.Program {
	if p.isInverse() {
		// inverseAndProgram
		return p.parseInverseAndProgram()
	}

	p.enter(p.next())
	defer p.leave()

	result := ast.NewProgram(p.next().Pos, p.next().Line)

	// openInverseChain
	block, blockParams := p.parseOpenBlock()

	// program
	program := p.parseProgram()

	program.BlockParams = blockParams
	block.Program = program

	// inverseChain?
	if p.isInverseChain() {
		block.Inverse = p.parseInverseChain()
	}

	setBlockInverseStrip(block)

	result.Chained = true
	result.AddStatement(block)

	return result
}

// Returns true if current token starts an inverse chain
func (p *parser) isInverseChain() bool {
	return p.isOpenInverseChain() || p.isInverse()
}

// inverseAndProgram : INVERSE program
func (p *parser) parseInverseAndProgram() *ast.Program {
	// INVERSE
	tok := p.shift()

	// program
	result := p.parseProgram()
	result.Strip = ast.NewStripForStr(tok.Val)

	return result
}

// openBlock : OPEN_BLOCK helperName param* hash? blockParams? CLOSE
// openInverse : OPEN_INVERSE helperName param* hash? blockParams? CLOSE
// openInverseChain: OPEN_INVERSE_CHAIN helperName param* hash? blockParams? CLOSE
func (p *parser) parseOpenBlock() (*ast.BlockStatement, []string) {
	// OPEN_BLOCK | OPEN_INVERSE | OPEN_INVERSE_CHAIN
	tok := p.shift()

	// helperName param* hash? blockParams?
	result, blockParams := p.parseOpenBlockExpression(tok)

	// CLOSE
	tokClose := p.shift()
	if tokClose.Kind != lexer.TokenClose {
		errExpected(lexer.TokenClose, tokClose)
	}

	result.OpenStrip = ast.NewStrip(tok.Val, tokClose.Val)

	// named returned values
	return result, blockParams
}

// closeBlock : OPEN_ENDBLOCK helperName CLOSE
func (p *parser) parseCloseBlock(block *ast.BlockStatement) {
	// OPEN_ENDBLOCK
	tok := p.shift()
	if tok.Kind != lexer.TokenOpenEndBlock {
		errExpected(lexer.TokenOpenEndBlock, tok)
	}

	// helperName
	endID := p.parseHelperName()

	closeName, ok := ast.HelperNameStr(endID)
	if !ok {
		errNode(endID, "Erroneous closing expression")
	}

	openName := block.Expression.Canonical()
	if openName != closeName {
		errNode(endID, fmt.Sprintf("%s doesn't match %s", openName, closeName))
	}

	// CLOSE
	tokClose := p.shift()
	if tokClose.Kind != lexer.TokenClose {
		errExpected(lexer.TokenClose, tokClose)
	}

	block.CloseStrip = ast.NewStrip(tok.Val, tokClose.Val)
}

// mustache : OPEN helperName param* hash? CLOSE
//
//	| OPEN_UNESCAPED helperName param* hash? CLOSE_UNESCAPED
func (p *parser) parseMustache() *ast.MustacheStatement {
	// OPEN | OPEN_UNESCAPED
	tok := p.shift()

	closeToken := lexer.TokenClose
	if tok.Kind == lexer.TokenOpenUnescaped {
		closeToken = lexer.TokenCloseUnescaped
	}

	unescaped := (tok.Kind == lexer.TokenOpenUnescaped) || isOpenAmp(tok.Val)

	result := ast.NewMustacheStatement(tok.Pos, tok.Line, unescaped)

	// helperName param* hash?
	result.Expression = p.parseExpression(tok)

	// CLOSE | CLOSE_UNESCAPED
	tokClose := p.shift()
	if tokClose.Kind != closeToken {
		errExpected(closeToken, tokClose)
	}

	result.Strip = ast.NewStrip(tok.Val, tokClose.Val)

	return result
}

// partial : OPEN_PARTIAL partialName param* hash? CLOSE
func (p *parser) parsePartial() *ast.PartialStatement {
	// OPEN_PARTIAL
	tok := p.shift()

	result := ast.NewPartialStatement(tok.Pos, tok.Line)

	// partialName
	result.Name = p.parsePartialName()

	// param* hash?
	result.Params, result.Hash = p.parseExpressionParamsHash()

	// CLOSE
	tokClose := p.shift()
	if tokClose.Kind != lexer.TokenClose {
		errExpected(lexer.TokenClose, tokClose)
	}

	result.Strip = ast.NewStrip(tok.Val, tokClose.Val)

	return result
}

// helperName | sexpr
func (p *parser) parseHelperNameOrSexpr() ast.Node {
	if p.isSexpr() {
		// sexpr
		return p.parseSexpr()
	}

	// helperName
	return p.parseHelperName()
}

// param : helperName | sexpr
func (p *parser) parseParam() ast.Node {
	return p.parseHelperNameOrSexpr()
}

// Returns true if next tokens represent a `param`
func (p *parser) isParam() bool {
	return (p.isSexpr() || p.isHelperName()) && !p.isHashSegment()
}

// param*
func (p *parser) parseParams() []ast.Node {
	var result []ast.Node

	for p.isParam() {
		result = append(result, p.parseParam())
	}

	return result
}

// sexpr : OPEN_SEXPR helperName param* hash? CLOSE_SEXPR
func (p *parser) parseSexpr() *ast.SubExpression {
	// OPEN_SEXPR
	tok := p.shift()
	p.enter(tok)
	defer p.leave()

	result := ast.NewSubExpression(tok.Pos, tok.Line)

	// helperName param* hash?
	result.Expression = p.parseExpression(tok)

	// CLOSE_SEXPR
	tok = p.shift()
	if tok.Kind != lexer.TokenCloseSexpr {
		errExpected(lexer.TokenCloseSexpr, tok)
	}

	return result
}

// hash : hashSegment+
func (p *parser) parseHash() *ast.Hash {
	var pairs []*ast.HashPair

	for p.isHashSegment() {
		pairs = append(pairs, p.parseHashSegment())
	}

	firstLoc := pairs[0].Location()

	result := ast.NewHash(firstLoc.Pos, firstLoc.Line)
	result.Pairs = pairs

	return result
}

// returns true if next tokens represents a `hashSegment`
func (p *parser) isHashSegment() bool {
	return p.have(2) && (p.next().Kind == lexer.TokenID) && (p.nextAt(1).Kind == lexer.TokenEquals)
}

// hashSegment : ID EQUALS param
func (p *parser) parseHashSegment() *ast.HashPair {
	// ID
	tok := p.shift()

	// EQUALS
	p.shift()

	// param
	param := p.parseParam()

	result := ast.NewHashPair(tok.Pos, tok.Line)
	result.Key = tok.Val
	result.Val = param

	return result
}

// blockParams : OPEN_BLOCK_PARAMS ID+ CLOSE_BLOCK_PARAMS
func (p *parser) parseBlockParams() []string {
	var result []string

	// OPEN_BLOCK_PARAMS
	p.shift()

	// ID+
	for p.isID() {
		result = append(result, p.shift().Val)
	}

	if len(result) == 0 {
		errExpected(lexer.TokenID, p.next())
	}

	// CLOSE_BLOCK_PARAMS
	tok := p.shift()
	if tok.Kind != lexer.TokenCloseBlockParams {
		errExpected(lexer.TokenCloseBlockParams, tok)
	}

	return result
}

// helperName : path | dataName | STRING | NUMBER | BOOLEAN | UNDEFINED | NULL
func (p *parser) parseHelperName() ast.Node {
	var result ast.Node

	tok := p.next()

	switch tok.Kind {
	case lexer.TokenBoolean:
		// BOOLEAN
		p.shift()
		result = ast.NewBooleanLiteral(tok.Pos, tok.Line, (tok.Val == "true"), tok.Val)
	case lexer.TokenNumber:
		// NUMBER
		p.shift()

		val, isInt := parseNumber(tok)
		result = ast.NewNumberLiteral(tok.Pos, tok.Line, val, isInt, tok.Val)
	case lexer.TokenString:
		// STRING
		p.shift()
		result = ast.NewStringLiteral(tok.Pos, tok.Line, tok.Val)
	case lexer.TokenData:
		// dataName
		result = p.parseDataName()
	default:
		// path
		result = p.parsePath(false)
	}

	return result
}

// parseNumber parses a number
func parseNumber(tok *lexer.Token) (float64, bool) {
	if valInt, err := strconv.Atoi(tok.Val); err == nil {
		return float64(valInt), true
	}

	result, err := strconv.ParseFloat(tok.Val, 64)
	if err != nil {
		errToken(tok, "Failed to parse number: "+tok.Val)
	}

	return result, false
}

// Returns true if next tokens represent a `helperName`
func (p *parser) isHelperName() bool {
	switch p.next().Kind {
	case lexer.TokenBoolean, lexer.TokenNumber, lexer.TokenString, lexer.TokenData, lexer.TokenID:
		return true
	default:
		return false
	}
}

// partialName : helperName | sexpr
func (p *parser) parsePartialName() ast.Node {
	return p.parseHelperNameOrSexpr()
}

// dataName : DATA pathSegments
func (p *parser) parseDataName() *ast.PathExpression {
	// DATA
	p.shift()

	// pathSegments
	return p.parsePath(true)
}

// path : pathSegments
// pathSegments : pathSegments SEP ID
//
//	| ID
func (p *parser) parsePath(data bool) *ast.PathExpression {
	var tok *lexer.Token

	// ID
	tok = p.shift()
	if tok.Kind != lexer.TokenID {
		errExpected(lexer.TokenID, tok)
	}

	result := ast.NewPathExpression(tok.Pos, tok.Line, data)

	// Build Original once: Part would copy it for every segment.
	var original strings.Builder
	original.WriteString(result.Original)
	original.WriteString(tok.Val)
	result.AddPart(tok.Val)

	for p.isPathSep() {
		// SEP
		tok = p.shift()
		original.WriteString(tok.Val)

		// ID
		tok = p.shift()
		if tok.Kind != lexer.TokenID {
			errExpected(lexer.TokenID, tok)
		}

		original.WriteString(tok.Val)
		result.AddPart(tok.Val)

		if len(result.Parts) > 0 {
			switch tok.Val {
			case "..", ".", "this":
				errToken(tok, "Invalid path: "+original.String())
			}
		}
	}

	result.Original = original.String()

	return result
}

// Ensures there is token to parse at given index
func (p *parser) ensure(index int) {
	if p.lexOver {
		// nothing more to grab
		return
	}

	nb := p.head + index + 1

	for len(p.tokens) < nb {
		// fetch next token
		if len(p.slab) == cap(p.slab) {
			// start small for small templates, then grow to tokenSlab
			p.slab = make([]lexer.Token, 0, min(max(2*cap(p.slab), 8), tokenSlab))
		}
		p.slab = append(p.slab, p.lex.NextToken())
		tok := &p.slab[len(p.slab)-1]

		// queue it
		p.tokens = append(p.tokens, tok)

		if (tok.Kind == lexer.TokenEOF) || (tok.Kind == lexer.TokenError) {
			p.lexOver = true
			break
		}
	}
}

// have returns true is there are a list given number of tokens to consume left
func (p *parser) have(nb int) bool {
	p.ensure(nb - 1)

	return len(p.tokens)-p.head >= nb
}

// nextAt returns next token at given index, without consuming it
func (p *parser) nextAt(index int) *lexer.Token {
	p.ensure(index)

	return p.tokens[p.head+index]
}

// next returns next token without consuming it
func (p *parser) next() *lexer.Token {
	return p.nextAt(0)
}

// shift returns next token and remove it from the tokens buffer
//
// Panics if next token is `TokenError`
func (p *parser) shift() *lexer.Token {
	var result *lexer.Token

	p.ensure(0)

	result = p.tokens[p.head]
	p.tokens[p.head] = nil
	p.head++
	if p.head == len(p.tokens) {
		// drained: reuse the buffer from its start
		p.tokens = p.tokens[:0]
		p.head = 0
	}

	// check error token
	if result.Kind == lexer.TokenError {
		errToken(result, "Lexer error")
	}

	return result
}

// isToken returns true if next token is of given type
func (p *parser) isToken(kind lexer.TokenKind) bool {
	return p.have(1) && p.next().Kind == kind
}

// isSexpr returns true if next token starts a sexpr
func (p *parser) isSexpr() bool {
	return p.isToken(lexer.TokenOpenSexpr)
}

// isPathSep returns true if next token is a path separator
func (p *parser) isPathSep() bool {
	return p.isToken(lexer.TokenSep)
}

// isID returns true if next token is an ID
func (p *parser) isID() bool {
	return p.isToken(lexer.TokenID)
}

// isBlockParams returns true if next token starts a block params
func (p *parser) isBlockParams() bool {
	return p.isToken(lexer.TokenOpenBlockParams)
}

// isInverse returns true if next token starts an INVERSE sequence
func (p *parser) isInverse() bool {
	return p.isToken(lexer.TokenInverse)
}

// isOpenInverseChain returns true if next token is OPEN_INVERSE_CHAIN
func (p *parser) isOpenInverseChain() bool {
	return p.isToken(lexer.TokenOpenInverseChain)
}
