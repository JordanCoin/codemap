; Rust query for functions and imports

; Function definitions
(function_item
  name: (identifier) @function)

; Method definitions in impl blocks
(function_item
  name: (identifier) @method)

; use statements
(use_declaration
  argument: (scoped_identifier) @import)

(use_declaration
  argument: (identifier) @import)

; mod declarations (internal modules)
(mod_item
  name: (identifier) @module)
