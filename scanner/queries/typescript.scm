; TypeScript/TSX query for functions and imports

; Function declarations
(function_declaration
  name: (identifier) @function)

; Arrow functions assigned to variables
(variable_declarator
  name: (identifier) @function
  value: (arrow_function))

; Method definitions
(method_definition
  name: (property_identifier) @function)

; Method signatures in interfaces
(method_signature
  name: (property_identifier) @function)

; ES6 imports
(import_statement
  source: (string) @import)
