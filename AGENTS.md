# Project instructions

## Language

- Write code, documentation and comments in English.
- The UI must support Portuguese. User-facing Portuguese strings are permitted; comments and technical documentation remain English.

## Documentation maintenance

- Every new feature must include updates to the affected documentation in the same change. Documentation is part of completing the feature.
- Keep the references aligned with changes to application behavior, consumed integrations/endpoints, fields/types/units, identifiers, matching/precedence/fallback rules, API/UI exposure, persistence, metrics, configuration and authentication.
- Update the canonical document for each affected topic:

| Topic | Canonical document |
|---|---|
| Project overview and quick start | [README.md](README.md) |
| Documentation index and reading paths | [docs/README.md](docs/README.md) |
| Runtime, consistency, durability and recovery | [docs/architecture.md](docs/architecture.md) |
| Entities, availability, provenance and time | [docs/data/README.md](docs/data/README.md) |
| Identity matching, enrichment and fallback | [docs/data/associations.md](docs/data/associations.md) |
| Historical collection, metrics, retention and limits | [docs/data/history.md](docs/data/history.md) |
| Integration inventory and source-to-field/lifecycle mappings | [docs/integrations/](docs/integrations/README.md) and the affected source reference |
| Deployment and operational configuration | [deploy/README.md](deploy/README.md) |
| Domain vocabulary only | [CONTEXT.md](CONTEXT.md) |

- Keep [api/openapi.yaml](api/openapi.yaml) authoritative for API operations and schemas. When the contract changes, update it and follow the existing generation workflow rather than manually editing generated Go/TypeScript files or maintaining duplicate schema lists in prose.
- New integrations must update the integration index, source reference, operator coverage and affected architecture/data flow. Changed parsing or matching must update the field tables, actual selection/rejection behavior and representative examples.
- Preserve existing research, plans and validation documents as dated evidence. Do not present them as the current application manual or rewrite their historical findings as newly verified.
- Describe implemented behavior, original source clocks, optional/missing values and limitations. Distinguish provider capability from collected data, cache from retained history, and stored fields from exposed API/UI fields. Do not invent observations, associations or metrics.
- Before declaring work complete, check the affected documents, local links, diagrams and code/schema references. Summarize documentation updates alongside implementation changes and report only checks actually performed.

## Documentation verification

- For documentation-only changes, use local link/reference checks, diagram parsing/rendering when available, and `git diff --check`; unrelated application builds/tests do not validate prose.
- Use existing behavior tests and fixtures to support claims. Run targeted application checks when needed to resolve a specific behavioral uncertainty or when implementation changes require them.
- Label synthetic examples and uncertain external claims. Keep dates on external evidence; code defaults, deployment settings, local budgets and provider guarantees are distinct.
