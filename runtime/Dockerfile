ARG BASE_IMAGE=python:3.13.7-slim-bookworm@sha256:adafcc17694d715c905b4c7bebd96907a1fd5cf183395f0ebc4d3428bd22d92d
FROM ${BASE_IMAGE}
ARG TARGETARCH
ARG SOURCE_DATE_EPOCH=1759190400
LABEL org.opencontainers.image.source="https://github.com/inframill/kit"
LABEL org.opencontainers.image.description="Inframill runtime with versioned Kit drivers, OpenTofu and Ansible Core"
# Signed Debian snapshot metadata fixes the OS dependency resolution date.
RUN printf 'deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/20260913T000000Z bookworm main\ndeb [check-valid-until=no] https://snapshot.debian.org/archive/debian-security/20260913T000000Z bookworm-security main\n' > /etc/apt/sources.list \
    && rm -f /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends openssh-client ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY requirements.lock python-sources.lock.json bundle.lock.json fetch-runtime.py /build/
RUN python3 -m venv /opt/inframill/tools/ansible/2.20.1 \
    && /opt/inframill/tools/ansible/2.20.1/bin/pip install --no-cache-dir --require-hashes -r /build/requirements.lock \
    && python3 /build/fetch-runtime.py ${TARGETARCH}
COPY bin/linux/${TARGETARCH}/ /usr/local/bin/
COPY config.json /etc/inframill/config.json
COPY sources/ /usr/share/inframill/sources/kit/
COPY NOTICES.md /usr/share/inframill/NOTICES.md
RUN mkdir -p /var/lib/inframill \
    && cp /build/bundle.lock.json /usr/share/inframill/bundle.lock.json \
    && dpkg-query -W > /usr/share/inframill/os-packages.txt \
    && inframill-kit version \
    && /opt/inframill/tools/opentofu/1.11.5/tofu version \
    && /opt/inframill/tools/ansible/2.20.1/bin/ansible-playbook --version
WORKDIR /var/lib/inframill
EXPOSE 8080
ENTRYPOINT ["inframill"]
CMD ["server", "--config", "/etc/inframill/config.json"]
