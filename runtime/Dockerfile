ARG BASE_IMAGE
FROM ${BASE_IMAGE}
ARG TARGETARCH
ARG SOURCE_DATE_EPOCH=1759190400
LABEL org.opencontainers.image.source="https://github.com/inframill/kit"
LABEL org.opencontainers.image.description="Inframill Kit with the engine, versioned drivers, OpenTofu and Ansible Core"
# Signed Debian snapshot metadata fixes the OS dependency resolution date.
RUN printf 'deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/20260913T000000Z bookworm main\ndeb [check-valid-until=no] https://snapshot.debian.org/archive/debian-security/20260913T000000Z bookworm-security main\n' > /etc/apt/sources.list \
    && rm -f /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends openssh-client ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY requirements.lock python-sources.lock.json bundle.lock.json fetch-runtime.py /build/
ARG ANSIBLE_VERSION
ARG OPENTOFU_VERSION
RUN python3 -m venv /opt/inframill/tools/ansible/${ANSIBLE_VERSION} \
    && /opt/inframill/tools/ansible/${ANSIBLE_VERSION}/bin/pip install --no-cache-dir --require-hashes -r /build/requirements.lock \
    && python3 /build/fetch-runtime.py ${TARGETARCH}
COPY bin/linux/${TARGETARCH}/ /usr/local/bin/
COPY inframill.yaml /etc/inframill/inframill.yaml
COPY sources/ /usr/share/inframill/sources/kit/
COPY NOTICES.md /usr/share/inframill/NOTICES.md
ARG INFRAMILL_VERSION
RUN mkdir -p /var/lib/inframill \
    && cp /build/bundle.lock.json /usr/share/inframill/bundle.lock.json \
    && dpkg-query -W > /usr/share/inframill/os-packages.txt \
    && test "$(inframill --version)" = "$INFRAMILL_VERSION" \
    && inframill-kit version \
    && /opt/inframill/tools/opentofu/${OPENTOFU_VERSION}/tofu version \
    && /opt/inframill/tools/ansible/${ANSIBLE_VERSION}/bin/ansible-playbook --version
WORKDIR /var/lib/inframill
EXPOSE 8080
ENTRYPOINT ["inframill"]
CMD ["server", "start", "--config", "/etc/inframill/inframill.yaml"]
