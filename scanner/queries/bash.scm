; Bash query for functions and source imports

; Function definitions
(function_definition
  name: (word) @function)

; source or . commands (imports)
(command
  name: (command_name) @_cmd
  argument: (word) @import
  (#match? @_cmd "^(source|\\.)$"))
