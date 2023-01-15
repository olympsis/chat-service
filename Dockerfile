FROM golang:1.19.1-bullseye
WORKDIR /app
COPY ./ ./
RUN go build -o /docker
RUN go mod download
ENV PORT=7011
ENV NIKOLA=1943
ENV DB_URL=mongodb://service:bWVzc2VuZ2VyIG9mIHRoZSBnb2Rz@hermes-db.olympsis.internal
ENV DB_NAME=hermes
ENV DB_COL=rooms
ENV KEY=SESHAT

EXPOSE 7011

CMD ["/docker"]


