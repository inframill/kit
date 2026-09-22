FROM python:3.13.7-slim-bookworm@sha256:adafcc17694d715c905b4c7bebd96907a1fd5cf183395f0ebc4d3428bd22d92d
ARG TARGETARCH
ARG SOURCE_DATE_EPOCH=1759190400
LABEL org.opencontainers.image.source="https://github.com/inframill/kit"
LABEL org.opencontainers.image.description="Inframill Kit with the engine, versioned providers, OpenTofu and Ansible Core"
ARG DEBIAN_SNAPSHOT=20260913T000000Z
# Signed Debian snapshot metadata fixes the OS dependency resolution date.
RUN printf "deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT} bookworm main\ndeb [check-valid-until=no] https://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT} bookworm-security main\n" > /etc/apt/sources.list \
    && rm -f /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends openssh-client ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY tools.py /build/

# Tool pins live here. Native setup reads these same literal ARG defaults.
ARG OPENTOFU_VERSION=1.11.5
ARG OPENTOFU_SHA256_LINUX_AMD64=901121681e751574d739de5208cad059eddf9bd739b575745cf9e3c961b28a13
ARG OPENTOFU_SHA256_LINUX_ARM64=1b79f8fe3cbf7ac0540507bb6bd0ecab1675c78393bbf492867eaabb79840eec
# Used only by native macOS test setup.
ARG OPENTOFU_SHA256_DARWIN_ARM64=ce81702643dcdea497259b4de4639d9ee73746560683b9d2b778aae8f7d62827
ARG OPENTOFU_SOURCE_SHA256=450f962f262d9f484ad1fb73454650740cdce0d83a854ad8b6c183cc5822eb09
ARG ANSIBLE_VERSION=2.20.1
ARG ANSIBLE_DEPENDENCIES="cffi==2.1.1 cryptography==50.0.1 Jinja2==3.1.6 MarkupSafe==3.0.3 packaging==26.3 pycparser==3.0 PyYAML==6.0.3 resolvelib==1.2.1"
RUN python3 -m venv /opt/inframill/tools/ansible/${ANSIBLE_VERSION} \
    && mkdir -p /usr/share/inframill \
    && /opt/inframill/tools/ansible/${ANSIBLE_VERSION}/bin/pip install --no-cache-dir \
       --report /usr/share/inframill/python-packages.json "ansible-core==${ANSIBLE_VERSION}" ${ANSIBLE_DEPENDENCIES} \
    && /opt/inframill/tools/ansible/${ANSIBLE_VERSION}/bin/pip check \
    && python3 /build/tools.py opentofu \
    && python3 /build/tools.py sources
COPY bin/linux/${TARGETARCH}/ /usr/local/bin/
COPY providers/${TARGETARCH}/ /opt/inframill/providers/
COPY inframill.yaml /etc/inframill/inframill.yaml
COPY sources/ /usr/share/inframill/sources/kit/
COPY NOTICES.md /usr/share/inframill/NOTICES.md
COPY bundle.lock.json /usr/share/inframill/bundle.lock.json
ARG INFRAMILL_VERSION
RUN mkdir -p /var/lib/inframill \
    && dpkg-query -W > /usr/share/inframill/os-packages.txt \
    && test "$(inframill --version)" = "$INFRAMILL_VERSION" \
    && /opt/inframill/tools/opentofu/${OPENTOFU_VERSION}/tofu version \
    && /opt/inframill/tools/ansible/${ANSIBLE_VERSION}/bin/ansible-playbook --version
RUN for provider in /opt/inframill/providers/*/*/provider; do "$provider" version || exit 1; done
WORKDIR /var/lib/inframill
EXPOSE 8080
ENTRYPOINT ["inframill"]
CMD ["server", "start", "--config", "/etc/inframill/inframill.yaml"]
