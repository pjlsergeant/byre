# Shell out to the engine CLI, not the Docker SDK

byre talks to Docker/Podman by shelling out to their CLIs behind a thin
runner interface covering only the operations it needs (build, run,
volume, image, container ops). Chosen over the Docker SDK for
Docker/Podman parity (two implementations of one small interface), zero
SDK coupling, and inspectability -- what byre runs is what you could type.

Consequences: Docker-touching logic is tested through injected runner
fakes; real-engine coverage lives in gated (`BYRE_DOCKER_TESTS=1`)
host-side integration tests.
