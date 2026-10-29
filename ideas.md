# Ideas

## Architecture graph as a planning and scope contract

A platform customer associates a factory with one repository or a collection of repositories. The factory builds a versioned architecture graph from those repositories: components, interfaces, dependencies, and the relationships between repositories. The graph is visually explorable so a person can understand the current system before starting work.

During planning, the factory presents the proposed change as a graph diff: which components and relationships will be added, changed, or removed, and why. The user can inspect and approve that intended scope alongside the plan.

The approved graph diff then becomes a scope contract for execution. The platform can use it to grant access to relevant areas, monitor code and contract changes, and assess whether the actual graph diverges from the plan. Deviations should be visible and explainable, with a way to approve a revised plan when new work is justified.

Start with visualization and drift reporting. Hard locks should come later, once graph extraction and change attribution are reliable enough to avoid blocking legitimate work. The graph is an aid to planning and review, not a claim that static analysis can perfectly describe a system.
