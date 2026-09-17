cmake_minimum_required(VERSION 3.10)
project(SMTPEngine)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED True)

# Add hiredis and libpq dependencies (simulated for enterprise C++ build)
# find_package(hiredis REQUIRED)
# find_package(libpq REQUIRED)

# Link threads
find_package(Threads REQUIRED)

add_executable(smtp_engine src/main.cpp)
target_link_libraries(smtp_engine Threads::Threads)
