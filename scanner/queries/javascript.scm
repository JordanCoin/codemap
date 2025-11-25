; JavaScript/JSX query for functions and imports

; Function declarations
(function_declaration
  name: (identifier) @function)

; Arrow functions assigned to variables
(variable_declarator
  name: (identifier) @function
  value: (arrow_function))

; Method definitions in classes/objects
(method_definition
  name: (property_identifier) @function)

; ES6 imports: import x from 'y'
(import_statement
  source: (string) @import)

; CommonJS: require('x')
(call_expression
  function: (identifier) @_req (#eq? @_req "require")
  arguments: (arguments (string) @import))
