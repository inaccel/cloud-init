FROM scratch
COPY cloud-init /bin/cloud-init
ENTRYPOINT ["cloud-init"]
