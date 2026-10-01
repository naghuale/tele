package telegram

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TDLib refuses a class it does not know, so a name this build invented is a
// read TDLib throws away — and it throws it away without saying so on the
// screen: the counter in the list simply stays where it was.
//
// That is not hypothetical. This package sent messageSourceChat and
// messageSourceHistory, and the pinned schema has neither of them
// (testdata/td_api.tl:3205), so every viewMessages of every kind of chat was
// refused. The recorded answers in the tests of the time were written by the
// same hand as the request and agreed with it.
//
// So the names are checked against the schema, and the schema is in the
// repository rather than remembered.

// pinnedSchema is the TL scheme of the pinned TDLib, taken from
// td/generate/scheme/td_api.tl at commit ea97bcdd3a15523c58ddfe772b4547187cf5bbeb
// (TDLib 1.8.67), which is the version telecli loads.
const pinnedSchema = "testdata/td_api.tl"

// tdlibNames returns every class, function and constructor of the pinned
// schema.
//
// A schema line is a name, fields, and `= Type;`, and the classes are the
// words before the first space. Anything that does not start with a lower
// case letter is a type, a vector or a section marker and not a name.
func tdlibNames(t *testing.T) map[string]bool {
	t.Helper()

	raw, err := os.ReadFile(pinnedSchema)
	if err != nil {
		t.Fatalf("read the pinned schema %s: %v", pinnedSchema, err)
	}

	names := make(map[string]bool)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" ||
			strings.HasPrefix(line, "//") ||
			strings.HasPrefix(line, "#") ||
			strings.HasPrefix(line, "-") {
			continue
		}

		name, _, _ := strings.Cut(line, " ")
		name = strings.TrimSpace(name)
		if !isTDLibName(name) {
			continue
		}
		names[name] = true
	}

	if len(names) < 1000 {
		t.Fatalf(
			"the pinned schema has %d names, which is far fewer than a TL "+
				"scheme has: is %s the whole file?",
			len(names), pinnedSchema,
		)
	}

	return names
}

// isTDLibName reports whether a word has the shape of a TDLib class name.
func isTDLibName(word string) bool {
	if len(word) < 2 {
		return false
	}
	for index, r := range word {
		switch {
		case index == 0:
			if r < 'a' || r > 'z' {
				return false
			}
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}

	return true
}

// tdlibNamespaces are the prefixes under which TDLib keeps its class names.
//
// A string literal that starts with one of them is a TDLib class name, or a
// prefix of one — auth.go matches authorizationState with strings.HasPrefix
// and there is no such class. Everything else in the package is the
// program's own vocabulary and is not this test's business.
var tdlibNamespaces = []string{
	"update",
	"messageSource",
	"chatType",
	"authorizationState",
	"connectionState",
}

// tdlibIntentionallyAbsent are the strings this package uses that are
// deliberately not classes of the pinned schema, and why.
//
// A test that proves a wrong answer is rejected has to name a wrong answer,
// and the wrong answer has to be a name TDLib does not have — otherwise the
// test would prove nothing about a name the schema has lost. Declaring them
// here is the price of that: a new one has to say why it is not a class.
var tdlibIntentionallyAbsent = map[string]string{
	"notChat":     "the answer of getChat that is not a chat",
	"notChats":    "the answer of getChats that is not a chat list",
	"notMessage":  "the answer of sendMessage that is not a message",
	"notMessages": "the answer of getChatHistory that is not a page",
	"connectionStateTeleporting": "a constructor the pinned schema does not " +
		"have, used to prove an unknown connection state is reported as unknown",
}

// TestEveryTDLibClassTheCodeNamesIsInThePinnedSchema walks the sources of the
// package and holds every string that names a TDLib class against the pinned
// schema.
//
// A class name reaches TDLib in two ways and both are covered: a literal in
// a position the schema names (a `@type` field of a request, or a name under
// the "@type" key of a map), and a literal inside one of TDLib's own
// namespaces, which is how the update and chat-type names of the decoders
// are written. The recorded answers of the tests are walked with the same
// rule, because a fixture that names a class this version does not have
// agrees with the decoder under test and with nothing else.
func TestEveryTDLibClassTheCodeNamesIsInThePinnedSchema(t *testing.T) {
	names := tdlibNames(t)

	// The declared names have to stay absent: a schema that grows one of them
	// turns a negative test into a test of nothing.
	for name, why := range tdlibIntentionallyAbsent {
		if names[name] {
			t.Errorf(
				"%q is declared as not a class (%s) and the pinned schema "+
					"has it: the test that uses it proves nothing now",
				name, why,
			)
		}
	}

	offenders := map[string][]string{}
	for _, cited := range tdlibClassLiterals(t, names) {
		if _, declared := tdlibIntentionallyAbsent[cited.name]; declared {
			continue
		}
		offenders[cited.name] = append(
			offenders[cited.name], cited.where,
		)
	}

	for name, places := range offenders {
		t.Errorf(
			"%q is not a class of the pinned schema (%s): %s",
			name, pinnedSchema, strings.Join(places, ", "),
		)
	}
}

// citedName is one string literal that names a TDLib class, and where it is.
type citedName struct {
	name  string
	where string
}

// tdlibClassLiterals returns the string literals of this package that name a
// TDLib class and are not in the schema.
//
// A literal qualifies when it sits in a @type position, or when it is inside
// one of TDLib's namespaces without being a class of the schema.
func tdlibClassLiterals(t *testing.T, names map[string]bool) []citedName {
	t.Helper()

	sources, fset := parseOwnSources(t)

	// One parse, two passes over the same tree: the second pass has to
	// recognise the literals the first one recorded, which it can only do
	// if both walks saw the same nodes.
	typeFields := collectTypeFields(sources)

	var cited []citedName
	for _, file := range sources {
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			word, err := strconv.Unquote(lit.Value)
			if err != nil || !isTDLibName(word) {
				return true
			}
			if names[word] {
				return true
			}
			if !typeFields.around(lit) && !unclaimedNamespace(word, names) {
				return true
			}

			position := fset.Position(lit.Pos())
			cited = append(cited, citedName{
				name: word,
				where: fmt.Sprintf(
					"%s:%d",
					filepath.Base(position.Filename),
					position.Line,
				),
			})

			return true
		})
	}

	return cited
}

// parseOwnSources parses every source file of this package once, tests
// included: a recorded answer is a claim about what TDLib sends as much as a
// request is a claim about what it takes.
func parseOwnSources(t *testing.T) ([]*ast.File, *token.FileSet) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}

	if len(files) == 0 {
		t.Fatal("no source of this package was parsed")
	}

	return files, fset
}

// unclaimedNamespace reports whether a word is inside one of TDLib's
// namespaces and is not a class of the schema.
//
// Only the namespace itself may be a prefix rather than a class: auth.go
// matches "authorizationState" with strings.HasPrefix and no class has that
// name. A longer word inside a namespace has to be a class, because that is
// what a class name is — and that is the rule that would have caught
// "messageSourceChat", which is a prefix of messageSourceChatHistory and a
// class of no version this build loads.
func unclaimedNamespace(word string, names map[string]bool) bool {
	for _, namespace := range tdlibNamespaces {
		if !strings.HasPrefix(word, namespace) {
			continue
		}
		if word == namespace {
			return false
		}
		for name := range names {
			if strings.HasPrefix(name, word) {
				return false
			}
		}

		return true
	}

	return false
}

// typeFields answers whether a literal sits in a @type position.
type typeFields struct {
	// structs maps the name of a struct that writes a `@type` field to the
	// name of that field.
	structs map[string]string

	positions map[*ast.BasicLit]bool
}

// collectTypeFields reads the sources and records the structs that write a
// "@type" field, and the literals of a composite literal that are the value
// of such a field or sit under an "@type" key of a map.
func collectTypeFields(files []*ast.File) typeFields {
	fields := typeFields{
		structs:   make(map[string]string),
		positions: make(map[*ast.BasicLit]bool),
	}

	// Two passes over the same tree: the struct declarations have to be
	// known before the composite literals are read, and the sources are
	// written in no particular order.
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			spec, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}
			structType, isStruct := spec.Type.(*ast.StructType)
			if !isStruct {
				return true
			}
			for _, field := range structType.Fields.List {
				if field.Tag == nil {
					continue
				}
				tag, err := strconv.Unquote(field.Tag.Value)
				if err != nil || tag != `json:"@type"` {
					continue
				}
				for _, name := range field.Names {
					fields.structs[spec.Name.Name] = name.Name
				}
			}

			return true
		})
	}

	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			fields.remember(lit)

			return true
		})
	}

	return fields
}

// remember records the @type literals of one composite literal.
func (f typeFields) remember(lit *ast.CompositeLit) {
	field := ""
	if ident, ok := lit.Type.(*ast.Ident); ok {
		field = f.structs[ident.Name]
	}

	for _, element := range lit.Elts {
		keyed, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		value, ok := keyed.Value.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			continue
		}

		switch key := keyed.Key.(type) {
		case *ast.Ident:
			if field != "" && key.Name == field {
				f.positions[value] = true
			}
		case *ast.BasicLit:
			word, err := strconv.Unquote(key.Value)
			if err == nil && word == "@type" {
				f.positions[value] = true
			}
		}
	}
}

// around reports whether a literal is in a @type position.
func (f typeFields) around(lit *ast.BasicLit) bool {
	return f.positions[lit]
}

// Every line of the schema this package's comments cite is checked against
// the file.
//
// A citation that names a line the schema does not have is the same mistake
// as a class that is not in the schema, and it is the kind of thing a reader
// stops believing after being wrong once: this package cited viewMessages at
// :13138, and viewMessages is at :13231.
func TestEveryCitedSchemaLineIsTheLineItNames(t *testing.T) {
	lines := readSchemaLines(t)

	for _, cited := range citations() {
		t.Run(cited.name, func(t *testing.T) {
			if cited.line < 1 || cited.line > len(lines) {
				t.Fatalf(
					"line %d is outside the %d lines of %s",
					cited.line, len(lines), pinnedSchema,
				)
			}

			line := strings.TrimSpace(lines[cited.line-1])
			if !strings.HasPrefix(line, cited.name+" ") {
				t.Errorf(
					"line %d is %q, not the %s the comment names",
					cited.line, line, cited.name,
				)
			}
		})
	}
}

// citations are the schema lines this package's comments name. A comment
// that cites a line is a claim about the pinned schema, and a claim about the
// schema that nobody checks is a claim that rots.
func citations() []struct {
	name string
	line int
} {
	return []struct {
		name string
		line int
	}{
		{name: "chatPosition", line: 3545},
		{name: "chat", line: 3628},
		{name: "messageSourceChatHistory", line: 3208},
		{name: "updateChatReadInbox", line: 10521},
		{name: "updateChatOnlineMemberCount", line: 10613},
		{name: "updateChatDraftMessage", line: 10539},
		{name: "updateChatLastMessage", line: 10507},
		{name: "updateChatPosition", line: 10512},
		{name: "updateNewChat", line: 10483},
		{name: "getChatHistory", line: 11830},
		{name: "openChat", line: 13220},
		{name: "closeChat", line: 13223},
		{name: "viewMessages", line: 13231},
		// The rights of a chat, as chat_access.go and live_state_access.go
		// read them.
		{name: "chatPermissions", line: 1070},
		{name: "chatAdministratorRights", line: 1092},
		{name: "user", line: 2403},
		{name: "chatMemberStatusCreator", line: 2493},
		{name: "chatMemberStatusAdministrator", line: 2500},
		{name: "chatMember", line: 2526},
		{name: "supergroup", line: 2747},
		{name: "chatTypePrivate", line: 3440},
		{name: "chatTypeSupergroup", line: 3446},
		{name: "updateChatPermissions", line: 10501},
		{name: "updateSupergroup", line: 10739},
		{name: "getUser", line: 11499},
		{name: "getSupergroup", line: 11511},
		// The types a message content is named by, the first and the last
		// of them: the labels of a message are named after them, and a
		// name that is not one of them is a label no message can carry
		// (message_content.go).
		{name: "messageText", line: 5141},
		{name: "messageDice", line: 5232},
		{name: "messageChatAddMembers", line: 5346},
		{name: "messageForumTopicIsClosedToggled", line: 5406},
		{name: "messageUnsupported", line: 5690},
	}
}

// readSchemaLines reads the pinned schema as lines.
func readSchemaLines(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(pinnedSchema)
	if err != nil {
		t.Fatalf("read the pinned schema %s: %v", pinnedSchema, err)
	}

	return strings.Split(string(raw), "\n")
}

// The source of a read is one class with no fields, and the schema says so.
// A field written into it would be a field TDLib ignores, and a source with
// a name of its own would be a name TDLib refuses.
func TestTheReadSourceIsTheOneTheSchemaHas(t *testing.T) {
	names := tdlibNames(t)

	if !names["messageSourceChatHistory"] {
		t.Fatal("the pinned schema has no messageSourceChatHistory")
	}
	if names["messageSourceChat"] || names["messageSourceHistory"] {
		t.Fatal(
			"the pinned schema has messageSourceChat or messageSourceHistory: " +
				"the schema of this build is not the one the comments cite",
		)
	}

	raw, err := json.Marshal(viewMessagesRequest{
		Type:       "viewMessages",
		ChatID:     42,
		MessageIDs: []tdInt{990, 991},
		Source:     viewMessagesSource,
		ForceRead:  true,
	})
	if err != nil {
		t.Fatalf("marshal viewMessages: %v", err)
	}

	var request struct {
		Source map[string]any `json:"source"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("decode viewMessages: %v", err)
	}
	if request.Source["@type"] != "messageSourceChatHistory" {
		t.Fatalf("source = %v, want messageSourceChatHistory", request.Source)
	}
	if len(request.Source) != 1 {
		t.Fatalf(
			"source = %v, want only its type: the schema declares no fields",
			request.Source,
		)
	}
}
