<!-- memoria:section id="overview" files="documentation/architecture.md documentation/memoria.md" -->
# Documentation

<!-- memoria:export id="summary" -->
Architecture rules, writing guidance, section conventions, and the Memoria review procedure for uagent.
<!-- /memoria:export -->

1. [Architecture](documentation/architecture.md): the layer rules every code change follows.
2. [Writing guidance](documentation/writing.md): voice, README shape, and what belongs where.
3. [Section IDs](documentation/sections.md): the shared IDs that map README sections to source files.
4. [Memoria procedure](documentation/memoria.md): how to review and acknowledge documentation after a code change.

Every README in this repository is maintained with [Memoria](https://github.com/viktordanov/rs-memoria).
The nearest README owns the files beneath it, and `memoria check` in CI fails when a README has not been reviewed against the current code.
<!-- /memoria:section -->
