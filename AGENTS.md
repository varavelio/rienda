# Project guidelines

## Summary

Rienda is a simple, easy to use, declarative, multi-agent LLM Harness, similar to Pi, OpenCode, etc.

## How to maintain this document

Keep this file current and minimal. Update it only when repository-wide workflow, structure, or project guidance changes. Do not turn it into a changelog. Use it exclusively to indicate truly relevant things in the codebase; don't include any minor details that are obvious or don't warrant documentation

## Required behavior

- It is imperative that you prioritize code readability and maintainability above all else; prefer simple, readable, and maintainable code over any unnecessary abstraction
- Always read `Taskfile.yml` to understand available `task` commands for the project; do not list those commands here
- When assigned a task, do not respond or stop until the requested task is complete
- Every time you finish your task run `task ci` for code checks. If it fails, fix failures caused by your own changes until it passes; stay within the scope of your changes and ignore pre-existing unrelated failures
- All code, code comments, inline documentation, commit messages, and any other text in the project MUST be written in English
- Any function, variable, constant, or other identifier must have a high-quality Godoc idiomatic comment written in plain English, following industry best practices and explaining its purpose and what it does (without including implementation details that are redundant when reading the code).

## Testing

Whenever possible, write tests that verify the expected behavior of the code being implemented. You must follow the following rules regarding testing:

- Write the unit tests close to the code they are testing; for example, if you have the file foo.go, you have to put all the unit tests inside foo_test.go
- When creating tests for Go, use the testify package which is already installed in the project. Prioritize using "require" whenever possible instead of "assert" so that the tests fail quickly when something is wrong
- Write high-value tests, focus on critical logic and relevant edge cases. Quality beats quantity; don't write tests just to inflate coverage; make sure every test adds real value.
- Treat tests as our primary tool to catch regressions. Write every test to guarantee long-term stability, correctness, functionality, and maintainability as the codebase evolves
- Group test cases with subtests (Go): Keep all test cases for a given function inside a single top-level Test function using subtests (t.Run). This maintains a clean structure and avoids file clutter when testing multiple functions in the same file
