package stack

// PolicyLocal never tracks origin's copy, as a landed pickup must not
var PolicyLocal = BranchPolicy{"refs/heads/"}
