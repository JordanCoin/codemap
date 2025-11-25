; Python query for extracting functions and imports

; Function definitions
(function_definition
  name: (identifier) @function)

; Class method definitions (also caught by function_definition)

; import x, import x.y.z
(import_statement
  name: (dotted_name) @import)

; from x import y
(import_from_statement
  module_name: (dotted_name) @import)

; from x import y, z (the module part)
(import_from_statement
  module_name: (relative_import) @import)
