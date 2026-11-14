# Trusted checker image example. Pin the base image digest and publish the
# resulting image by immutable digest, then add that digest to
# ACTFB_AD_RUNNER_IMAGES. Never use a mutable tag in production.
FROM python:3.13-alpine@sha256:9ef1a599f863231c3d91e941389bfdc4a2d6070557243d54df1a4e57015b960e

COPY check.py /usr/local/bin/actfb-check
RUN chmod 0555 /usr/local/bin/actfb-check

USER 65534:65534
WORKDIR /work
ENTRYPOINT ["python3", "/usr/local/bin/actfb-check"]