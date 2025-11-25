; Java query for functions and imports

; Method declarations
(method_declaration
  name: (identifier) @function)

; Constructor declarations
(constructor_declaration
  name: (identifier) @function)

; Import declarations
(import_declaration
  (scoped_identifier) @import)
