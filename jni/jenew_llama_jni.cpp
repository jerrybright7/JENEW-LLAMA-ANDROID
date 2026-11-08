#include <jni.h>
#include <android/log.h>
#include <llama.h>

#include <algorithm>
#include <atomic>
#include <cstdint>
#include <memory>
#include <mutex>
#include <string>
#include <stdexcept>
#include <vector>

#define LOG_TAG "JENEW-LLAMA"
#define LOGE(...) __android_log_print(ANDROID_LOG_ERROR, LOG_TAG, __VA_ARGS__)

namespace {
struct ModelHandle {
    llama_model * model = nullptr;
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
    if (modelPath == nullptr) {
        throwJava(env, "Model path is null.");
        return 0;
    }

    const char * path = env->GetStringUTFChars(modelPath, nullptr);
    if (path == nullptr) return 0;

    llama_backend_init();
    llama_model_params modelParams = llama_model_default_params();
    modelParams.n_gpu_layers = 0;
    llama_model * model = llama_model_load_from_file(path, modelParams);
    env->ReleaseStringUTFChars(modelPath, path);

    if (model == nullptr) {
        throwJava(env, "llama.cpp could not load the GGUF model.");
        return 0;
    }

    auto * handle = new ModelHandle();
    handle->model = model;
    (void) contextSize;
    (void) threads;
    return reinterpret_cast<jlong>(handle);
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

        const uint32_t requestedContext = static_cast<uint32_t>(
                std::max<jint>(128, contextSize));
        const uint32_t outputLimit = static_cast<uint32_t>(
                std::max<jint>(1, maxTokens));
        if (promptTokens.size() + outputLimit > requestedContext) {
            throwJava(env, "Prompt plus requested output exceeds the configured context size.");
            return nullptr;
        }

        llama_context_params ctxParams = llama_context_default_params();
        ctxParams.n_ctx = requestedContext;
        ctxParams.n_batch = std::max<uint32_t>(512,
                static_cast<uint32_t>(promptTokens.size()));
        ctxParams.n_batch = std::min(ctxParams.n_batch, requestedContext);
        ctxParams.n_ubatch = std::min<uint32_t>(512, ctxParams.n_batch);
        ctxParams.n_threads = std::max<jint>(1, threads);
        ctxParams.n_threads_batch = std::max<jint>(1, threads);

        llama_context * ctx = llama_init_from_model(handle->model, ctxParams);
        if (ctx == nullptr) {
            throwJava(env, "Could not create llama.cpp inference context.");
            return nullptr;
        }

        std::unique_ptr<llama_context, decltype(&llama_free)> context(ctx, &llama_free);
        llama_batch batch = llama_batch_get_one(promptTokens.data(),
                static_cast<int32_t>(promptTokens.size()));
        if (llama_decode(ctx, batch) != 0) {
            throwJava(env, "llama.cpp failed to evaluate the prompt.");
            return nullptr;
        }

        llama_sampler_chain_params samplerParams = llama_sampler_chain_default_params();
        llama_sampler * sampler = llama_sampler_chain_init(samplerParams);
        if (temperature <= 0.001f) {
            llama_sampler_chain_add(sampler, llama_sampler_init_greedy());
        } else {
            llama_sampler_chain_add(sampler, llama_sampler_init_temp(temperature));
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
