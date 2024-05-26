package cn.scutbot.sim.rmdispatcher.service.impl

import cn.scutbot.sim.rmdispatcher.config.LarkConfig
import cn.scutbot.sim.rmdispatcher.data.lark.MessageCard
import cn.scutbot.sim.rmdispatcher.data.lark.MessageCardData
import cn.scutbot.sim.rmdispatcher.data.lark.WebhookCard
import cn.scutbot.sim.rmdispatcher.data.lark.newTemplateMessageCard
import cn.scutbot.sim.rmdispatcher.service.ILarkService
import cn.scutbot.sim.rmdispatcher.utils.logger
import com.lark.oapi.Client
import com.lark.oapi.service.im.v1.enums.CreateImageImageTypeEnum
import com.lark.oapi.service.im.v1.enums.CreateMessageReceiveIdTypeEnum
import com.lark.oapi.service.im.v1.model.*
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.web.client.RestTemplateBuilder
import org.springframework.cache.annotation.Cacheable
import org.springframework.scheduling.annotation.Async
import org.springframework.stereotype.Service
import org.springframework.util.FileCopyUtils
import org.springframework.web.client.RestTemplate
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CompletableFuture
import kotlin.io.path.deleteIfExists
import kotlin.io.path.pathString


@Service
class LarkServiceImpl(
    @Autowired val larkClient: Client,
    @Autowired val larkConfig: LarkConfig,
    @Autowired val restTemplateBuilder: RestTemplateBuilder
) : ILarkService {
    val restTemplate: RestTemplate by lazy {
        restTemplateBuilder.build()
    }

    @Async
    override fun sendMessage(
        receiverIdType: CreateMessageReceiveIdTypeEnum,
        receiverId: String,
        msgType: String,
        content: String
    ): CompletableFuture<String> {
        val msgBody = CreateMessageReqBody.newBuilder()
            .receiveId(receiverId)
            .msgType(msgType)
            .content(content)
            .build()
        val msgReq = CreateMessageReq.newBuilder()
            .receiveIdType(receiverIdType)
            .createMessageReqBody(msgBody)
            .build()
        val resp = larkClient.im().message().create(msgReq)

        if (!resp.success()) {
            logger().error("Send message failed: ${resp.code} ${resp.msg}")
            return CompletableFuture.completedFuture("")
        }

        return CompletableFuture.completedFuture(resp.data.messageId)
    }

    override fun webhooks(): Set<String> = larkConfig.webhooks

    override fun <T> sendWebhookCard(url: String, cardId: String, vars: T) {
        val card = WebhookCard(card = MessageCard(data=MessageCardData(cardId, vars)))

        restTemplate.postForLocation(url, card)
    }

    @Cacheable("lark-groups")
    override fun joinedGroups(): Set<String> {
        var pageToken: String? = null
        val groups = mutableSetOf<String>()

        do {
            val resp = larkClient.im().chat().list(ListChatReq.newBuilder().pageToken(pageToken).build())
            if (!resp.success()) {
                logger().error("List chat failed: ${resp.code} ${resp.msg}")
                return emptySet()
            }

            groups += resp.data.items.map { it.chatId }
            pageToken = resp.data.pageToken
        } while (resp.data.hasMore)

        return groups
    }

    @Cacheable("larkImg-url", cacheManager = "holdingCacheManager")
    override fun uploadImg(sourceUrl: String?): String? {
        if (sourceUrl == null)
            return null
        val path = Files.createTempFile("img-rmdispatcher", ".png").also {
            logger().info("Created temp file ${it.pathString}")
        }
        val restTemplate = RestTemplate()
        val resp = restTemplate.getForEntity(sourceUrl, ByteArray::class.java)
        if (!resp.statusCode.is2xxSuccessful || resp.body == null) {
            logger().warn("Get $sourceUrl failed: ${resp.statusCode.value()}")
            return null
        }

        FileCopyUtils.copy(resp.body!!, path.toFile())

        val id = uploadImg(path)
        path.deleteIfExists()
        return id
    }

    override fun uploadImg(file: Path): String? {
        val imgBody = CreateImageReqBody.newBuilder()
            .imageType(CreateImageImageTypeEnum.MESSAGE)
            .image(file.toFile())
            .build()
        val imgReq = CreateImageReq.newBuilder()
            .createImageReqBody(imgBody)
            .build()
        val resp = larkClient.im().image().create(imgReq)

        return if (resp.success()) {
            resp.data.imageKey
        } else {
            logger().warn("Upload img ${file.pathString} code ${resp.code}: ${resp.msg}")
            null
        }
    }

    @Async
    override fun <T> updateNotifyCard(messageIds: Set<String>, cardId: String, vars: T) {
        val card = newTemplateMessageCard(cardId, vars)

        messageIds.forEach {
            updateCard(it, card)
        }
    }

    @Async
    fun updateCard(messageId: String, content: String) {
        val req = PatchMessageReq.newBuilder()
            .messageId(messageId)
            .patchMessageReqBody(
                PatchMessageReqBody.newBuilder()
                    .content(content)
                    .build()
            )
            .build()


        val resp: PatchMessageResp = larkClient.im().message().patch(req)

        if (!resp.success()) {
            logger().error("Update message failed: ${resp.code} ${resp.msg}")
        }
    }
}
