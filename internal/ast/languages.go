package ast

import "strings"

// DetectLanguage returns the language name for a file extension.
// Returns "" for unsupported files.
func DetectLanguage(filename string) string {
	ext := strings.ToLower(filename)
	if idx := strings.LastIndex(ext, "."); idx >= 0 {
		ext = ext[idx:]
	}
	switch ext {
	case ".go":
		return "Go"
	case ".py", ".pyw":
		return "Python"
	case ".js", ".mjs", ".cjs":
		return "JavaScript"
	case ".jsx":
		return "JSX"
	case ".ts":
		return "TypeScript"
	case ".tsx":
		return "TSX"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".rb", ".rake":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".c", ".h":
		return "C"
	case ".cpp", ".cxx", ".cc", ".hh", ".hpp":
		return "C++"
	case ".cs":
		return "C#"
	case ".kt", ".kts":
		return "Kotlin"
	case ".swift":
		return "Swift"
	case ".scala":
		return "Scala"
	case ".lua":
		return "Lua"
	case ".zig":
		return "Zig"
	case ".ex", ".exs":
		return "Elixir"
	case ".hs":
		return "Haskell"
	case ".dart":
		return "Dart"
	case ".sh", ".bash":
		return "Bash"
	case ".r":
		return "R"
	default:
		return ""
	}
}

// IsSourceFile returns true if the file extension represents parseable source code.
func IsSourceFile(filename string) bool {
	return DetectLanguage(filename) != ""
}

// SupportedLanguages returns all languages pincherMCP can extract symbols from.
func SupportedLanguages() []string {
	return []string{
		"Go", "Python", "JavaScript", "JSX", "TypeScript", "TSX",
		"Rust", "Java", "Ruby", "PHP", "C", "C++", "C#",
		"Kotlin", "Swift", "Scala", "Dart", "Bash", "Elixir",
	}
}
