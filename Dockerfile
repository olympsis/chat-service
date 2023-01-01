FROM golang:1.19.1-bullseye
WORKDIR /app
COPY ./ ./
RUN go build -o /docker
RUN go mod download
ENV PORT=7011
ENV NIKOLA=1943
ENV KEY=SESHAT

EXPOSE 7011

CMD ["/docker"]