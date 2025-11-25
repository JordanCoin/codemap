; Go query for extracting functions and imports

; Function declarations
(function_declaration
  name: (identifier) @function)

; Method declarations (functions with receivers)
(method_declaration
  name: (field_identifier) @function)

; Import paths
(import_spec
  path: (interpreted_string_literal) @import)
