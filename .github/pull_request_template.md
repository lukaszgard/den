# Pull Request

## Description
<!-- Provide a short summary of the changes -->

### Contents

- [ ] Feature
- [ ] Bugfix
- [ ] Refactor
- [ ] Documentation
- [ ] Configuration
- [ ] Pipeline
- [ ] Other

<hr>

## How to Test
<!-- Describe how reviewers can verify the changes if not obvious -->
<!-- Attach screenshots if it helps other contributors -->
<hr>

## Checklist
<!-- Ensure all items are completed before requesting review -->
Mandatory items:
<br>
- [ ] Commit messages follow `<type>: <summary>` (feat, fix, refactor, perf, test, docs, build, ci)
- [ ] PR was tested:
    - [ ] Locally
    - [ ] Against a real AWS account
    - [ ] Other
    - [ ] No testing necessary
- [ ] Tests created?
    - [ ] Unit tests
    - [ ] Integration tests
    - [ ] No, because:
        - [ ] No implementation changes were introduced
        - [ ] *_other reason:_* <!-- mandatory reason here -->
    - [ ] Only updated existing tests, because
        - [ ] Changes do not bring new test cases
        - [ ]  *_other reason:_* <!-- mandatory reason here -->

### Configuration changed?
<!-- Document changes to den.yaml keys, environment variables or file locations -->
- [ ] `den.yaml` keys or defaults changed:
    - [ ] Schema, validation and the annotated `den.yaml` updated
    - [ ] `make generate` run
- [x] Not relevant for or not in scope of this PR

### Documentation updated?
- [ ] Yes, in this repository (see PR file list)
- [x] Not relevant for or not in scope of this PR

## Breaking Changes
<!-- Describe breaking changes and migration steps, if any -->
- [x] None
