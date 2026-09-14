# Inframill Kit

Official drivers, blueprints, and runtime packaging for Inframill.

The planned `inframill-kit` Go executable provides OpenTofu and Ansible Core
drivers. Blueprints such as `pv-cloud` are published separately as OCI artifacts.
A Linux runtime container bundles Inframill, the drivers, and pinned tool versions.

| Component | Name |
| --- | --- |
| Repository | `inframill/kit` |
| Go binary | `inframill-kit` |
| Runtime image repository | `inframill/runtime` |
| pv-cloud OCI repository | `inframill/blueprints/pv-cloud` |

Registry repositories are relative to the selected registry host. Build, test,
bundle, and blueprint publication workflows will use Taskfile targets.

Implementation has not started. See [PLAN.md](PLAN.md) for the current draft.
