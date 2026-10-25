# The binary is built outside and copied in, so the image contains nothing
# but the program. FROM scratch is an empty filesystem.
FROM scratch
COPY golab /golab
# An empty directory owned by that user; a volume mounted here starts out
# with the same ownership.
COPY --chown=10001:10001 data /data
# A numeric user, because the image has no /etc/passwd to look a name up in.
USER 10001:10001
EXPOSE 8080
# The ledger lives in a volume mounted at /data, not in the image.
WORKDIR /data
ENTRYPOINT ["/golab", "-ledger", "/data/golab-ledger.jsonl"]
CMD ["serve", "-addr", "0.0.0.0:8080"]
