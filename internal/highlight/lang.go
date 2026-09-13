// Package highlight is a small, allocation-conscious syntax highlighter.
//
// It exists instead of a general-purpose library because the preview pane only
// needs six token classes, and a single linear pass over the bytes is both
// smaller in the binary and an order of magnitude faster on large files than a
// regex-driven lexer.
package highlight

import (
	"path"
	"strings"
	"sync"
)

// Lang describes just enough of a language's lexical surface to colour it.
type Lang struct {
	Name string

	kwSrc, tySrc string // space-separated, expanded once on first use

	LineComment []string
	BlockOpen   string
	BlockClose  string
	Quotes      string   // characters that open a single-line string
	Raw         []string // delimiters for multi-line/raw strings (open == close)
	NoEscape    bool     // backslash is literal inside strings (e.g. Go raw, shell single)

	once   sync.Once
	kw, ty map[string]struct{}
}

func (l *Lang) sets() (map[string]struct{}, map[string]struct{}) {
	l.once.Do(func() {
		l.kw = toSet(l.kwSrc)
		l.ty = toSet(l.tySrc)
	})
	return l.kw, l.ty
}

func toSet(s string) map[string]struct{} {
	f := strings.Fields(s)
	m := make(map[string]struct{}, len(f))
	for _, w := range f {
		m[w] = struct{}{}
	}
	return m
}

const (
	cFamilyLine  = "//"
	cFamilyOpen  = "/*"
	cFamilyClose = "*/"
)

var (
	langGo = &Lang{
		Name:        "Go",
		kwSrc:       "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false iota",
		tySrc:       "bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any comparable",
		LineComment: []string{cFamilyLine}, BlockOpen: cFamilyOpen, BlockClose: cFamilyClose,
		Quotes: "\"'", Raw: []string{"`"},
	}
	langRust = &Lang{
		Name:        "Rust",
		kwSrc:       "as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while",
		tySrc:       "bool char f32 f64 i8 i16 i32 i64 i128 isize str u8 u16 u32 u64 u128 usize String Vec Option Result Box Rc Arc HashMap",
		LineComment: []string{cFamilyLine}, BlockOpen: cFamilyOpen, BlockClose: cFamilyClose,
		Quotes: "\"'",
	}
	langC = &Lang{
		Name:        "C/C++",
		kwSrc:       "auto break case class const constexpr continue default delete do else enum explicit export extern false for friend goto if inline namespace new nullptr operator private protected public register return sizeof static struct switch template this throw true try typedef typename union using virtual volatile while include define ifdef ifndef endif pragma",
		tySrc:       "bool char double float int long short signed unsigned void size_t uint8_t uint16_t uint32_t uint64_t int8_t int16_t int32_t int64_t string vector map",
		LineComment: []string{cFamilyLine, "#"}, BlockOpen: cFamilyOpen, BlockClose: cFamilyClose,
		Quotes: "\"'",
	}
	langJS = &Lang{
		Name:        "JavaScript/TypeScript",
		kwSrc:       "abstract as async await break case catch class const continue debugger declare default delete do else enum export extends false finally for from function get if implements import in instanceof interface let new null of private protected public readonly return satisfies set static super switch this throw true try type typeof undefined var void while with yield",
		tySrc:       "any bigint boolean never number object string symbol unknown Array Promise Record Map Set Date RegExp Error JSON Math console window document",
		LineComment: []string{cFamilyLine}, BlockOpen: cFamilyOpen, BlockClose: cFamilyClose,
		Quotes: "\"'", Raw: []string{"`"},
	}
	langPython = &Lang{
		Name:        "Python",
		kwSrc:       "and as assert async await break class continue def del elif else except False finally for from global if import in is lambda None nonlocal not or pass raise return True try while with yield match case self cls",
		tySrc:       "bool bytes complex dict float frozenset int list object set str tuple type Any Dict List Optional Tuple Union Callable Iterator",
		LineComment: []string{"#"},
		Quotes:      "\"'", Raw: []string{`"""`, "'''"},
	}
	langRuby = &Lang{
		Name:        "Ruby",
		kwSrc:       "alias and begin break case class def defined? do else elsif end ensure false for if in module next nil not or redo rescue retry return self super then true undef unless until when while yield attr_accessor attr_reader require require_relative",
		tySrc:       "Array Hash String Symbol Integer Float Struct Module Class Proc Range",
		LineComment: []string{"#"},
		Quotes:      "\"'",
	}
	langJava = &Lang{
		Name:        "Java/Kotlin",
		kwSrc:       "abstract as assert break by case catch class companion const continue crossinline data default do else enum extends false final finally for fun if implements import in infix init inline instanceof interface internal is lateinit native new null object open operator out override package private protected public reified return sealed static super suspend switch synchronized this throw throws transient true try typealias val var vararg volatile when while",
		tySrc:       "boolean byte char double float int long short void Any Boolean Byte Char Double Float Int List Long Map Nothing Set Short String Unit Array",
		LineComment: []string{cFamilyLine}, BlockOpen: cFamilyOpen, BlockClose: cFamilyClose,
		Quotes: "\"'", Raw: []string{`"""`},
	}
	langSwift = &Lang{
		Name:        "Swift",
		kwSrc:       "as associatedtype async await break case catch class continue default defer deinit do else enum extension fallthrough false fileprivate for func guard if import in init inout internal is let nil open operator private protocol public repeat rethrows return self Self static struct subscript super switch throw throws true try typealias var where while",
		tySrc:       "Any AnyObject Array Bool Character Data Dictionary Double Float Int Optional Set String UInt Void",
		LineComment: []string{cFamilyLine}, BlockOpen: cFamilyOpen, BlockClose: cFamilyClose,
		Quotes: "\"", Raw: []string{`"""`},
	}
	langShell = &Lang{
		Name:        "Shell",
		kwSrc:       "if then else elif fi case esac for while until do done function in select time coproc return break continue local export readonly declare typeset source alias unalias set unset shift trap exit eval exec test echo printf cd",
		tySrc:       "true false",
		LineComment: []string{"#"},
		Quotes:      "\"'", NoEscape: false,
	}
	langSQL = &Lang{
		Name:        "SQL",
		kwSrc:       "ADD ALL ALTER AND AS ASC BEGIN BETWEEN BY CASCADE CASE CHECK COLUMN COMMIT CONSTRAINT CREATE CROSS DEFAULT DELETE DESC DISTINCT DROP ELSE END EXISTS FOREIGN FROM FULL GROUP HAVING IF IN INDEX INNER INSERT INTO IS JOIN KEY LEFT LIKE LIMIT NOT NULL OFFSET ON OR ORDER OUTER PRIMARY REFERENCES RETURNING RIGHT ROLLBACK SELECT SET TABLE THEN TRANSACTION TRUNCATE UNION UNIQUE UPDATE USING VALUES VIEW WHEN WHERE WITH and as asc between by case create delete desc distinct drop from group having in inner insert into is join left limit not null on or order select set table union update values where with",
		tySrc:       "bigint boolean bytea char date decimal double float int integer json jsonb numeric real serial smallint text time timestamp uuid varchar",
		LineComment: []string{"--"}, BlockOpen: cFamilyOpen, BlockClose: cFamilyClose,
		Quotes: "'\"",
	}
	langPHP = &Lang{
		Name:        "PHP",
		kwSrc:       "abstract and array as break callable case catch class clone const continue declare default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile enum extends final finally fn for foreach function global goto if implements include include_once instanceof insteadof interface isset list match namespace new or print private protected public readonly require require_once return static switch throw trait try unset use var while xor yield true false null",
		tySrc:       "bool float int iterable mixed object string void self parent",
		LineComment: []string{cFamilyLine, "#"}, BlockOpen: cFamilyOpen, BlockClose: cFamilyClose,
		Quotes: "\"'",
	}
	langLua = &Lang{
		Name:        "Lua",
		kwSrc:       "and break do else elseif end false for function goto if in local nil not or repeat return then true until while",
		tySrc:       "string table math io os coroutine require pairs ipairs print type tostring tonumber",
		LineComment: []string{"--"}, BlockOpen: "--[[", BlockClose: "]]",
		Quotes: "\"'", Raw: []string{"[["},
	}
	langCSS = &Lang{
		Name:        "CSS",
		kwSrc:       "import media supports keyframes font-face charset namespace include mixin extend use forward if else each for while return",
		tySrc:       "auto none inherit initial unset flex grid block inline absolute relative fixed sticky hidden visible solid dashed dotted bold normal center left right",
		BlockOpen:   cFamilyOpen,
		BlockClose:  cFamilyClose,
		LineComment: []string{cFamilyLine},
		Quotes:      "\"'",
	}
	langJSON = &Lang{
		Name:   "JSON",
		kwSrc:  "true false null",
		Quotes: "\"",
	}
	langYAML = &Lang{
		Name:        "YAML",
		kwSrc:       "true false null yes no on off",
		LineComment: []string{"#"},
		Quotes:      "\"'",
	}
	langTOML = &Lang{
		Name:        "TOML",
		kwSrc:       "true false",
		LineComment: []string{"#"},
		Quotes:      "\"'", Raw: []string{`"""`, "'''"},
	}
	langHTML = &Lang{
		Name:       "HTML/XML",
		kwSrc:      "html head body div span a p ul ol li table tr td th form input button script style link meta title img svg path section header footer nav main article aside h1 h2 h3 h4 h5 h6 template slot",
		BlockOpen:  "<!--",
		BlockClose: "-->",
		Quotes:     "\"'",
	}
	langHCL = &Lang{
		Name:        "HCL/Terraform",
		kwSrc:       "resource provider variable output module data locals terraform for_each count depends_on lifecycle dynamic if for in true false null",
		tySrc:       "string number bool list map set object tuple any",
		LineComment: []string{"#", cFamilyLine}, BlockOpen: cFamilyOpen, BlockClose: cFamilyClose,
		Quotes: "\"", Raw: []string{"<<EOT"},
	}
	langMake = &Lang{
		Name:        "Makefile",
		kwSrc:       "ifeq ifneq ifdef ifndef else endif include define endef export unexport override vpath .PHONY .DEFAULT",
		LineComment: []string{"#"},
		Quotes:      "\"'",
	}
	langDocker = &Lang{
		Name:        "Dockerfile",
		kwSrc:       "FROM RUN CMD LABEL MAINTAINER EXPOSE ENV ADD COPY ENTRYPOINT VOLUME USER WORKDIR ARG ONBUILD STOPSIGNAL HEALTHCHECK SHELL AS",
		LineComment: []string{"#"},
		Quotes:      "\"'",
	}
	langZig = &Lang{
		Name:        "Zig",
		kwSrc:       "align allowzero and anyframe anytype asm async await break catch comptime const continue defer else enum errdefer error export extern fn for if inline noalias nosuspend or orelse packed pub resume return struct suspend switch test threadlocal try union unreachable usingnamespace var volatile while true false null undefined",
		tySrc:       "bool c_int c_uint f16 f32 f64 i8 i16 i32 i64 isize noreturn type u8 u16 u32 u64 usize void anyerror",
		LineComment: []string{cFamilyLine},
		Quotes:      "\"'", Raw: []string{`\\`},
	}
	langMarkdown = &Lang{Name: "Markdown"}
	langPlain    = &Lang{Name: "Text"}
)

var byExt = map[string]*Lang{
	".go": langGo, ".mod": langGo, ".sum": langPlain,
	".rs": langRust,
	".c":  langC, ".h": langC, ".cc": langC, ".cpp": langC, ".cxx": langC,
	".hpp": langC, ".hh": langC, ".m": langC, ".mm": langC, ".cu": langC,
	".js": langJS, ".jsx": langJS, ".mjs": langJS, ".cjs": langJS,
	".ts": langJS, ".tsx": langJS, ".mts": langJS, ".cts": langJS,
	".vue": langJS, ".svelte": langJS, ".astro": langJS,
	".py": langPython, ".pyi": langPython, ".pyx": langPython,
	".rb": langRuby, ".rake": langRuby, ".gemspec": langRuby,
	".java": langJava, ".kt": langJava, ".kts": langJava, ".scala": langJava, ".groovy": langJava,
	".swift": langSwift,
	".sh":    langShell, ".bash": langShell, ".zsh": langShell, ".fish": langShell, ".ksh": langShell,
	".sql": langSQL,
	".php": langPHP,
	".lua": langLua,
	".css": langCSS, ".scss": langCSS, ".sass": langCSS, ".less": langCSS,
	".json": langJSON, ".jsonc": langJSON, ".json5": langJSON, ".ndjson": langJSON,
	".yaml": langYAML, ".yml": langYAML,
	".toml": langTOML,
	".html": langHTML, ".htm": langHTML, ".xml": langHTML, ".xhtml": langHTML,
	".svg": langHTML, ".plist": langHTML, ".xsl": langHTML,
	".tf": langHCL, ".tfvars": langHCL, ".hcl": langHCL, ".nomad": langHCL,
	".mk":  langMake,
	".zig": langZig,
	".md":  langMarkdown, ".markdown": langMarkdown, ".mdx": langMarkdown,
	".txt": langPlain, ".log": langPlain, ".csv": langPlain, ".tsv": langPlain,
	".proto": langC, ".gradle": langJava, ".dart": langJava,
	".ini": langTOML, ".cfg": langTOML, ".conf": langTOML, ".env": langShell,
	".el": langLua, ".vim": langPlain, ".r": langPlain, ".jl": langPython,
	".ex": langRuby, ".exs": langRuby, ".erl": langPlain, ".hs": langPlain,
	".pl": langPerlish, ".pm": langPerlish,
}

// langPerlish reuses the shell shape; Perl's sigils and # comments line up.
var langPerlish = langShell

var byName = map[string]*Lang{
	"makefile": langMake, "gnumakefile": langMake, "dockerfile": langDocker,
	"containerfile": langDocker, "justfile": langMake, "rakefile": langRuby,
	"gemfile": langRuby, "vagrantfile": langRuby, "brewfile": langRuby,
	"cmakelists.txt": langMake, ".bashrc": langShell, ".zshrc": langShell,
	".profile": langShell, ".gitignore": langYAML, ".dockerignore": langYAML,
	"go.mod": langGo, "go.sum": langPlain, "package.json": langJSON,
}

// Detect picks a lexer from the file name, falling back to plain text.
func Detect(name string) *Lang {
	base := strings.ToLower(path.Base(name))
	if l, ok := byName[base]; ok {
		return l
	}
	if strings.HasPrefix(base, "dockerfile") {
		return langDocker
	}
	if l, ok := byExt[path.Ext(base)]; ok {
		return l
	}
	return langPlain
}
