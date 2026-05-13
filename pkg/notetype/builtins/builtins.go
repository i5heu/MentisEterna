// Package builtins blank-imports all built-in note types so they self-register.
package builtins

import (
	_ "github.com/i5heu/MentisEterna/pkg/notetype/example"
	_ "github.com/i5heu/MentisEterna/pkg/notetype/index"
	_ "github.com/i5heu/MentisEterna/pkg/notetype/recipe"
	_ "github.com/i5heu/MentisEterna/pkg/notetype/recipeoverview"
)
