; C query for functions and imports

; Function definitions
(function_definition
  declarator: (function_declarator
    declarator: (identifier) @function))

; Function declarations (prototypes)
(declaration
  declarator: (function_declarator
    declarator: (identifier) @function))

; #include directives
(preproc_include
  path: (string_literal) @import)

(preproc_include
  path: (system_lib_string) @import)
