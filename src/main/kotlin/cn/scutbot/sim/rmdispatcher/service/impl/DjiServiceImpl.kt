package cn.scutbot.sim.rmdispatcher.service.impl

import cn.scutbot.sim.rmdispatcher.data.dji.CurrentAndNextMatch
import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.service.IDjiService
import cn.scutbot.sim.rmdispatcher.utils.logger
import com.fasterxml.jackson.databind.ObjectMapper
import com.jayway.jsonpath.Configuration
import com.jayway.jsonpath.JsonPath
import com.jayway.jsonpath.TypeRef
import com.jayway.jsonpath.spi.json.JacksonJsonProvider
import com.jayway.jsonpath.spi.mapper.JacksonMappingProvider
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.web.client.RestTemplateBuilder
import org.springframework.cache.annotation.Cacheable
import org.springframework.http.*
import org.springframework.stereotype.Service
import org.springframework.web.client.RestTemplate
import org.springframework.web.client.exchange

const val DJI_SCHEDULE_URL = "https://pro-robomasters-hz-n5i3.oss-cn-hangzhou.aliyuncs.com/live_json/schedule.json"
const val DJI_TEAMS_URL = "https://pro-robomasters-hz-n5i3.oss-cn-hangzhou.aliyuncs.com/live_json/teams.json"
const val DJI_API_URL = "https://pro-robomasters-hz-n5i3.oss-cn-hangzhou.aliyuncs.com/live_json/current_and_next_matches.json"

@Service
class DjiServiceImpl(
    @Autowired val templateBuilder: RestTemplateBuilder,
    @Autowired val mapper: ObjectMapper,
) : IDjiService {
    val restTemplate: RestTemplate by lazy {
        templateBuilder.build()
    }

    override fun fetchInfo(): List<CurrentAndNextMatch> {
        val headers = HttpHeaders()
        headers.accept = listOf(MediaType.APPLICATION_JSON)

        val resp: ResponseEntity<List<CurrentAndNextMatch>> = restTemplate.exchange(
            DJI_API_URL,
            HttpMethod.GET
        )

        return resp.body ?: emptyList()
    }

    val jsonPathConfig: Configuration by lazy {
        Configuration.defaultConfiguration()
            .jsonProvider(JacksonJsonProvider(mapper))
            .mappingProvider(JacksonMappingProvider(mapper))
    }

    @Cacheable("match-schedule")
    override fun fetchSchedule(match: Match): Match? {
        val resp = restTemplate.exchange<String>(
            DJI_SCHEDULE_URL,
            HttpMethod.GET,
            HttpEntity.EMPTY
        )

        if (!resp.statusCode.is2xxSuccessful || resp.body == null)
            return null

        val json = JsonPath.parse(resp.body, jsonPathConfig)

        val pathMapped = when(match.matchType) {
            "KNOCKOUT" -> {
                "knockoutMatches"
            }
            "GROUP" -> "groupMatches"
            else -> ""
        }

        val ref = object : TypeRef<List<Match>>() {}
        val matched = json.read(
            "$.data.event.zones.nodes[?(@.id == '${match.zone?.id}')]" +
                    ".${pathMapped}.nodes[?(@.id == '${match.id}')]", ref).firstOrNull()
        matched?.zone = match.zone

        return matched
    }

    @Cacheable("colleges", cacheManager = "holdingCacheManager")
    override fun collegeFullNames(): List<String> {
        logger().info("Refreshing $DJI_TEAMS_URL")
        val resp = restTemplate.exchange(
            DJI_TEAMS_URL,
            HttpMethod.GET,
            HttpEntity.EMPTY,
            String::class.java
        )

        if (!resp.statusCode.is2xxSuccessful || resp.body == null) {
            logger().warn("Failed to fetch $DJI_TEAMS_URL: ${resp.statusCode.value()}")
        }

        val json = JsonPath.parse(resp.body, Configuration.defaultConfiguration()
            .jsonProvider(JacksonJsonProvider(mapper))
            .mappingProvider(JacksonMappingProvider(mapper)))

        val ref = object : TypeRef<List<String>>() {}
        val result = json.read("$.data.event.zones.nodes[*].teamZones.nodes[*].team.collegeName", ref)

        return result
    }

    @Cacheable("zones", cacheManager = "holdingCacheManager")
    override fun zones(): List<String> {
        logger().info("Refreshing $DJI_SCHEDULE_URL")
        val resp = restTemplate.exchange<String>(
            DJI_SCHEDULE_URL,
            HttpMethod.GET,
            HttpEntity.EMPTY
        )

        if (!resp.statusCode.is2xxSuccessful || resp.body == null) {
            logger().warn("Failed to fetch $DJI_SCHEDULE_URL: ${resp.statusCode.value()}")
        }

        val json = JsonPath.parse(resp.body, Configuration.defaultConfiguration()
            .jsonProvider(JacksonJsonProvider(mapper))
            .mappingProvider(JacksonMappingProvider(mapper)))

        val ref = object : TypeRef<List<String>>() {}
        val result = json.read("$.data.event.zones.nodes[*].name", ref)

        return result
    }
}
