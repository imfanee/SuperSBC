# sipp 3.7 with TLS (OpenSSL 3), PCAP and RTP streaming. The public 3.5.1
# images cannot complete a TLS 1.2 handshake with FreeSWITCH.
FROM debian:bookworm-slim AS build
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
    build-essential cmake git libssl-dev libpcap-dev libncurses-dev libsctp-dev libgsl-dev ca-certificates pkg-config >/dev/null
ARG SIPP_VERSION=v3.7.3
RUN git clone -q --depth 1 --branch ${SIPP_VERSION} https://github.com/SIPp/sipp.git /src \
 && cd /src && git submodule update --init --recursive -q && sed -i "s/-Werror//g" CMakeLists.txt \
 && cmake -DUSE_SSL=1 -DUSE_PCAP=1 -DUSE_SCTP=1 -DUSE_GSL=1 -DCMAKE_BUILD_TYPE=Release -DCMAKE_CXX_FLAGS="-Wno-error" . >/dev/null \
 && make -j"$(nproc)" >/dev/null && strip sipp

FROM debian:bookworm-slim
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends libssl3 libpcap0.8 libncursesw6 libsctp1 libgsl27 >/dev/null && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/sipp /usr/local/bin/sipp
WORKDIR /out
ENTRYPOINT ["sipp"]
