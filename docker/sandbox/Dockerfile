ARG TRUSTED_BASE_IMAGE
FROM ${TRUSTED_BASE_IMAGE}
USER root
RUN apk del git \
    && rm -rf /usr/libexec/git-core \
    && mkdir -p /etc/cdo \
    && touch /etc/cdo/gitconfig \
    && chmod 0444 /etc/cdo/gitconfig
COPY --chmod=0755 deny-git.sh /usr/bin/git
RUN ln -sf /usr/bin/git /usr/local/bin/git
USER 100:101
