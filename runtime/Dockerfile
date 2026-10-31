ARG BASE_IMAGE
FROM ${BASE_IMAGE}
ARG TARGETARCH
ARG SOURCE_DATE_EPOCH
ARG DEBIAN_SNAPSHOT
LABEL org.opencontainers.image.source="https://github.com/inframill/kit"
LABEL org.opencontainers.image.description="Inframill with Kit providers, OpenTofu and Ansible"
RUN printf "deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT} bookworm main\ndeb [check-valid-until=no] https://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT} bookworm-security main\n" > /etc/apt/sources.list \
    && rm -f /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends openssh-client ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY runtime/tools.py runtime/dependencies.json /build/
RUN mkdir -p /usr/share/inframill
RUN python3 /build/tools.py ansible \
    && python3 /build/tools.py opentofu \
    && python3 /build/tools.py sources
COPY bin/linux/${TARGETARCH}/ /usr/local/bin/
COPY inframill.yaml /etc/inframill/inframill.yaml
COPY sources.tar.gz /usr/share/inframill/sources/kit.tar.gz
COPY notices/ /usr/share/inframill/notices/
COPY runtime/NOTICES.md /usr/share/inframill/NOTICES.md
COPY manifest.json /usr/share/inframill/manifest.json
ARG INFRAMILL_VERSION
ARG KIT_VERSION
RUN mkdir -p /var/lib/inframill \
    && dpkg-query -W > /usr/share/inframill/os-packages.txt \
    && test "$(inframill --version)" = "$INFRAMILL_VERSION" \
    && test "$(/usr/local/bin/kit-exec version)" = "{\"version\":\"$KIT_VERSION\"}" \
    && /usr/local/bin/kit-exec list
WORKDIR /var/lib/inframill
EXPOSE 8080
ENTRYPOINT ["inframill"]
CMD ["server", "start", "--config", "/etc/inframill/inframill.yaml"]
