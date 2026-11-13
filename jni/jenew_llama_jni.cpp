#include <jni.h>
#include <android/log.h>
#include <llama.h>
#include <ggml-backend.h>

#include <algorithm>
#include <atomic>
#include <cstdint>
#include <climits>
#include <memory>
#include <mutex>
#include <string>
#include <stdexcept>
#include <vector>

#define LOG_TAG "JENEW-LLAMA"
#define LOGE(...) __android_log_print(ANDROID_LOG_ERROR, LOG_TAG, __VA_ARGS__)

namespace {
constexpr uint32_t JENEW_MIN_CONTEXT = 8192;
constexpr uint32_t JENEW_MIN_BATCH = 512;
constexpr uint32_t JENEW_MAX_BATCH = 1024;

struct ModelHandle {
    llama_model * model = nullptr;
    uint32_t contextSize = JENEW_MIN_CONTEXT;
    uint32_t modelContextLimit = 0;
    int32_t threads = 6;
    int32_t modelLayers = 0;
    std::mutex mutex;
    std::atomic<bool> cancelled{false};
};

void throwJava(JNIEnv * env, const char * message) {
    jclass cls = env->FindClass("java/lang/IllegalStateException");
    if (cls != nullptr) env->ThrowNew(cls, message);
}

std::vector<llama_token> tokenize(const llama_vocab * vocab, const std::string & text) {
    int32_t capacity = std::max<int32_t>(256, static_cast<int32_t>(text.size()) + 16);
    std::vector<llama_token> tokens(static_cast<size_t>(capacity));
    int32_t count = llama_tokenize(vocab, text.c_str(),
            static_cast<int32_t>(text.size()), tokens.data(), capacity, true, true);
    if (count < 0) {
        capacity = -count;
        tokens.resize(static_cast<size_t>(capacity));
        count = llama_tokenize(vocab, text.c_str(),
                static_cast<int32_t>(text.size()), tokens.data(), capacity, true, true);
    }
    if (count < 0) throw std::runtime_error("Prompt tokenization failed.");
    tokens.resize(static_cast<size_t>(count));
    return tokens;
}

std::string tokenPiece(const llama_vocab * vocab, llama_token token) {
    std::vector<char> buffer(256);
    int32_t count = llama_token_to_piece(vocab, token, buffer.data(),
            static_cast<int32_t>(buffer.size()), 0, false);
    if (count < 0) {
        buffer.resize(static_cast<size_t>(-count));
        count = llama_token_to_piece(vocab, token, buffer.data(),
                static_cast<int32_t>(buffer.size()), 0, false);
    }
    if (count <= 0) return {};
    return std::string(buffer.data(), static_cast<size_t>(count));
}
}

extern "C" JNIEXPORT jlong JNICALL
Java_com_jenew_smartdc_JenewLlamaNative_nativeCreate(
        JNIEnv * env, jobject, jstring modelPath, jint contextSize, jint threads) {
    llama_backend_init();
    return reinterpret_cast<jlong>(createModelHandle(env, modelPath, contextSize, threads, 0));
}

extern "C" JNIEXPORT jint JNICALL
Java_com_jenew_smartdc_JenewLlamaNative_nativeTokenCount(
        JNIEnv * env, jobject, jlong nativeHandle, jstring prompt) {
    auto * handle = reinterpret_cast<ModelHandle *>(nativeHandle);
    if (handle == nullptr || handle->model == nullptr || prompt == nullptr) {
        throwJava(env, "Local model is not loaded or prompt is empty.");
        return -1;
    }

    const char * chars = env->GetStringUTFChars(prompt, nullptr);
    if (chars == nullptr) return -1;

    std::string text(chars);
    env->ReleaseStringUTFChars(prompt, chars);

    std::lock_guard<std::mutex> lock(handle->mutex);

    try {
        const llama_vocab * vocab = llama_model_get_vocab(handle->model);
        const auto tokens = tokenize(vocab, text);

        if (tokens.size() > static_cast<size_t>(INT32_MAX)) {
            throw std::runtime_error("Prompt token count exceeds JNI integer limit.");
        }

        return static_cast<jint>(tokens.size());
    } catch (const std::exception & error) {
        LOGE("Token counting error: %s", error.what());
        throwJava(env, error.what());
        return -1;
    } catch (...) {
        throwJava(env, "Unknown native tokenization error.");
        return -1;
    }
}

extern "C" JNIEXPORT jstring JNICALL
Java_com_jenew_smartdc_JenewLlamaNative_nativeGenerate(
        JNIEnv * env, jobject, jlong nativeHandle, jstring prompt,
        jint maxTokens, jfloat temperature, jint contextSize, jint threads) {
    auto * handle = reinterpret_cast<ModelHandle *>(nativeHandle);
    if (handle == nullptr || handle->model == nullptr || prompt == nullptr) {
        throwJava(env, "Local model is not loaded or prompt is empty.");
        return nullptr;
    }

    const char * promptChars = env->GetStringUTFChars(prompt, nullptr);
    if (promptChars == nullptr) return nullptr;
    std::string promptText(promptChars);
    env->ReleaseStringUTFChars(prompt, promptChars);

    std::lock_guard<std::mutex> lock(handle->mutex);
    handle->cancelled.store(false);

    try {
        const llama_vocab * vocab = llama_model_get_vocab(handle->model);
        std::vector<llama_token> promptTokens = tokenize(vocab, promptText);
        if (promptTokens.empty()) {
            throwJava(env, "Prompt produced no tokens.");
            return nullptr;
        }

        const uint32_t requestedContext = std::max(
                handle->contextSize,
                static_cast<uint32_t>(std::max<jint>(128, contextSize)));
        if (requestedContext > handle->modelContextLimit) {
            throwJava(env, "Requested context exceeds this GGUF model's supported context size.");
            return nullptr;
        }

        const uint32_t outputLimit = static_cast<uint32_t>(
                std::max<jint>(1, maxTokens));
        if (promptTokens.size() + outputLimit > requestedContext) {
            const std::string message = "Prompt uses " + std::to_string(promptTokens.size())
                    + " tokens and output reserves " + std::to_string(outputLimit)
                    + "; the configured context is " + std::to_string(requestedContext)
                    + ". Reduce the prompt or output limit.";
            throwJava(env, message.c_str());
            return nullptr;
        }

        llama_context_params ctxParams = llama_context_default_params();
        ctxParams.n_ctx = requestedContext;
        // Keep the decode batch bounded: large prompts are evaluated in chunks below.
        ctxParams.n_batch = std::min<uint32_t>(requestedContext,
                std::max<uint32_t>(JENEW_MIN_BATCH,
                        std::min<uint32_t>(JENEW_MAX_BATCH,
                                static_cast<uint32_t>(promptTokens.size()))));
        ctxParams.n_ubatch = std::min<uint32_t>(512, ctxParams.n_batch);
        ctxParams.n_threads = std::max<jint>(1, threads > 0 ? threads : handle->threads);
        ctxParams.n_threads_batch = ctxParams.n_threads;

        llama_context * ctx = llama_init_from_model(handle->model, ctxParams);
        if (ctx == nullptr) {
            throwJava(env, "Could not create llama.cpp inference context for the requested context size.");
            return nullptr;
        }

        std::unique_ptr<llama_context, decltype(&llama_free)> context(ctx, &llama_free);
        const size_t promptBatchSize = static_cast<size_t>(ctxParams.n_batch);
        for (size_t offset = 0; offset < promptTokens.size(); offset += promptBatchSize) {
            const size_t remaining = promptTokens.size() - offset;
            const int32_t chunkSize = static_cast<int32_t>(
                    std::min(promptBatchSize, remaining));
            llama_batch batch = llama_batch_get_one(promptTokens.data() + offset, chunkSize);
            if (llama_decode(ctx, batch) != 0) {
                throwJava(env, "llama.cpp failed to evaluate a prompt chunk.");
                return nullptr;
            }
        }

        llama_sampler_chain_params samplerParams = llama_sampler_chain_default_params();
        llama_sampler * sampler = llama_sampler_chain_init(samplerParams);
        if (temperature <= 0.001f) {
            llama_sampler_chain_add(sampler, llama_sampler_init_greedy());
        } else {
            llama_sampler_chain_add(sampler, llama_sampler_init_temp(temperature));
            llama_sampler_chain_add(sampler, llama_sampler_init_top_p(0.9f, 1));
            llama_sampler_chain_add(sampler, llama_sampler_init_dist(LLAMA_DEFAULT_SEED));
        }
        std::unique_ptr<llama_sampler, decltype(&llama_sampler_free)> samplerGuard(
                sampler, &llama_sampler_free);

        std::string output;
        output.reserve(static_cast<size_t>(outputLimit) * 8);
        for (uint32_t i = 0; i < outputLimit; ++i) {
            if (handle->cancelled.load()) break;

            const llama_token token = llama_sampler_sample(sampler, ctx, -1);
            llama_sampler_accept(sampler, token);
            if (llama_vocab_is_eog(vocab, token)) break;

            output += tokenPiece(vocab, token);
            if (i + 1 < outputLimit) {
                llama_token nextToken = token;
                llama_batch nextBatch = llama_batch_get_one(&nextToken, 1);
                if (llama_decode(ctx, nextBatch) != 0) break;
            }
        }
        return env->NewStringUTF(output.c_str());
    } catch (const std::exception & error) {
        LOGE("Generation error: %s", error.what());
        throwJava(env, error.what());
        return nullptr;
    } catch (...) {
        throwJava(env, "Unknown native inference error.");
        return nullptr;
    }
}

extern "C" JNIEXPORT void JNICALL
Java_com_jenew_smartdc_JenewLlamaNative_nativeCancel(
        JNIEnv *, jobject, jlong nativeHandle) {
    auto * handle = reinterpret_cast<ModelHandle *>(nativeHandle);
    if (handle != nullptr) handle->cancelled.store(true);
}

extern "C" JNIEXPORT void JNICALL
Java_com_jenew_smartdc_JenewLlamaNative_nativeDestroy(
        JNIEnv *, jobject, jlong nativeHandle) {
    auto * handle = reinterpret_cast<ModelHandle *>(nativeHandle);
    if (handle == nullptr) return;
    {
        std::lock_guard<std::mutex> lock(handle->mutex);
        if (handle->model != nullptr) {
            llama_model_free(handle->model);
            handle->model = nullptr;
        }
    }
    delete handle;
}
