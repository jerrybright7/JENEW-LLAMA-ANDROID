cmake_minimum_required(VERSION 3.22.1)
project(jenewllama LANGUAGES CXX)

if(NOT DEFINED LLAMA_CPP_ROOT OR NOT DEFINED LLAMA_LIB_DIR)
    message(FATAL_ERROR "LLAMA_CPP_ROOT and LLAMA_LIB_DIR are required")
endif()

set(LLAMA_LIBRARY "${LLAMA_LIB_DIR}/libllama.so")
set(GGML_LIBRARY "${LLAMA_LIB_DIR}/libggml.so")
set(GGML_BASE_LIBRARY "${LLAMA_LIB_DIR}/libggml-base.so")
if(NOT EXISTS "${LLAMA_LIBRARY}")
    message(FATAL_ERROR "Missing llama shared library: ${LLAMA_LIBRARY}")
endif()
if(NOT EXISTS "${GGML_LIBRARY}")
    message(FATAL_ERROR "Missing ggml backend registry library: ${GGML_LIBRARY}")
endif()
if(NOT EXISTS "${GGML_BASE_LIBRARY}")
    message(FATAL_ERROR "Missing ggml-base shared library: ${GGML_BASE_LIBRARY}")
endif()

add_library(llama SHARED IMPORTED GLOBAL)
set_target_properties(llama PROPERTIES
    IMPORTED_LOCATION "${LLAMA_LIBRARY}"
    INTERFACE_INCLUDE_DIRECTORIES
        "${LLAMA_CPP_ROOT}/include;${LLAMA_CPP_ROOT}/ggml/include"
)
add_library(ggml SHARED IMPORTED GLOBAL)
set_target_properties(ggml PROPERTIES
    IMPORTED_LOCATION "${GGML_LIBRARY}"
    INTERFACE_INCLUDE_DIRECTORIES "${LLAMA_CPP_ROOT}/ggml/include"
)
add_library(ggml-base SHARED IMPORTED GLOBAL)
set_target_properties(ggml-base PROPERTIES
    IMPORTED_LOCATION "${GGML_BASE_LIBRARY}"
    INTERFACE_INCLUDE_DIRECTORIES "${LLAMA_CPP_ROOT}/ggml/include"
)

find_library(ANDROID_LOG_LIBRARY log REQUIRED)

add_library(jenewllama SHARED jenew_llama_jni.cpp)
target_compile_features(jenewllama PRIVATE cxx_std_17)
target_compile_options(jenewllama PRIVATE -Wall -Wextra -Werror=return-type)
target_link_libraries(jenewllama PRIVATE llama ggml ggml-base ${ANDROID_LOG_LIBRARY})
set_target_properties(jenewllama PROPERTIES
    OUTPUT_NAME "jenewllama"
    CXX_VISIBILITY_PRESET hidden
    VISIBILITY_INLINES_HIDDEN YES
)
