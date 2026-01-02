# feed.tiulp.in (glance)

dev:
    docker compose up --build

build:
    docker build -t glance .

stop:
    docker compose down

logs:
    docker compose logs -f

deploy:
    fly deploy

status:
    fly status

