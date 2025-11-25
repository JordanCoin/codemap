; C# query for methods and using directives

; Method declarations
(method_declaration
  name: (identifier) @function)

; Constructor declarations
(constructor_declaration
  name: (identifier) @function)

; Using directives
(using_directive
  (qualified_name) @import)

(using_directive
  (identifier) @import)
