FROM gradle:8.6.0-jdk21-alpine as build
WORKDIR /app
COPY . .
RUN gradle build
WORKDIR /app/build/libs
RUN java -Djarmode=layertools -jar app.jar extract

FROM amazoncorretto:21-alpine
ENV TZ=Asia/Shanghai
WORKDIR /app
COPY --from=build /app/build/libs/dependencies/ ./
COPY --from=build /app/build/libs/spring-boot-loader/ ./
COPY --from=build /app/build/libs/snapshot-dependencies/ ./
COPY --from=build /app/build/libs/application/ ./

ENTRYPOINT ["java", "org.springframework.boot.loader.launch.JarLauncher"]