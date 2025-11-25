; PHP query for functions and imports

; Function definitions
(function_definition
  name: (name) @function)

; Method declarations
(method_declaration
  name: (name) @function)

; Use statements (imports)
(namespace_use_clause
  (qualified_name) @import)

; Require/include statements
(include_expression
  (string) @import)

(include_once_expression
  (string) @import)

(require_expression
  (string) @import)

(require_once_expression
  (string) @import)
