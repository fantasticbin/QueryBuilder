# QueryBuilder

简化 Go 项目中的列表查询，支持灵活的过滤、排序、分页和策略扩展，适用于多种数据源（如 GORM、Mongo等）。

---

## 特性

- **通用查询接口**：统一的列表查询入口，支持多种数据源。
- **灵活的过滤与排序**：通过泛型和选项模式自定义过滤条件和排序方式。
- **中间件机制**：支持查询链路中插入自定义逻辑（如耗时统计、日志记录等）。
- **易于测试**：内置 Mock 支持，便于单元测试。
- **可扩展性强**：策略模式，便于扩展新的数据源或查询方式。
- **支持控制分页开关**：可应用于导出数据。

---

## 安装

```shell
go get github.com/fantasticbin/querybuilder
```

---

## 快速开始

### 1. 定义pb协议和过滤/排序结构

```protobuf
syntax = "proto3";

service User {
  rpc ListUser (ListUserRequest) returns (ListUserReply) {
    option (google.api.http) = {
      get: "/users"
    };
  };
}

message ListUserRequest {
  ListUserFilter filter = 1;
  QueryUserListSort sort = 2;
  uint32 start = 3;
  uint32 limit = 4;
}

message ListUserReply {
  repeated UserInfo users = 1;
  uint32 total = 2;
}

message ListUserFilter {
  string name = 1;
  UserStatus status = 2;
  string created_at = 3;
}

enum QueryUserListSort {
  CREATED_AT = 0;
  AGE = 1;
}

enum UserGender {
  UNKNOWN = 0;
  MALE = 1;
  FEMALE = 2;
}

enum UserStatus {
  NONE = 0;
  NORMAL = 1;
  BAN = 2;
}

message UserInfo {
  uint64 id = 1;
  string name = 2;
  uint32 age = 3;
  UserGender gender = 4;
  UserStatus status = 5;
  string created_at = 6;
}
```

### 2. 创建 Service

```go
package service

import "context"

type UserService struct{}

func (s *UserService) GetFilter(ctx context.Context) (any, error) { 
    // 完善过滤逻辑
}

func (s *UserService) GetSort() any {
    // 完善排序逻辑
}
```

### 3. 使用 QueryBuilder 查询

```go
package service

import (
    "context"
    pb "demo/api/user/v1"
    "demo/internal/model"
)

func ListUser(ctx context.Context, req *pb.ListUserRequest) ([]*model.User, int64, error) { 
    // 定义类型别名，简化泛型类型的推断
    type filter = pb.ListUserFilter
    type sort = pb.QueryUserListSort

    list := NewList[model.User, filter, sort](&UserService{})
    result, total, err := list.Query(
        ctx,
        WithData[filter, sort](NewDBProxy(model.db, nil)),
        WithFilter[filter, sort](req.Filter),
        WithSort[filter, sort](req.Sort),
        WithStart[filter, sort](req.Start),
        WithLimit[filter, sort](req.Limit),
    )
    if err != nil {
        return nil, 0, err
    }

    return result, total, nil
}
```

---

## 进阶用法

### 中间件

支持在查询链路中插入自定义中间件：

```go
package service

import (
    "context"
    pb "demo/api/user/v1"
    "demo/internal/model"
)

func ListUser(ctx context.Context, req *pb.ListUserRequest) ([]*model.User, int64, error) { 
    // 定义类型别名，简化泛型类型的推断
    type filter = pb.ListUserFilter
    type sort = pb.QueryUserListSort

    list := NewList[model.User, filter, sort](&UserService{})
    list.Use(func(
        ctx context.Context,
        builder *builder[TestEntity],
        next func(context.Context,
        ) ([]*TestEntity, int64, error)) ([]*TestEntity, int64, error) {
        defer func() func() {
            pre := time.Now()
            return func() {
                elapsed := time.Since(pre)
                fmt.Println("elapsed:", elapsed)
            }
        }()()
        
        result, total, err := next(ctx)
        return result, total, err
    })
    result, total, err := list.Query(
        ctx,
        WithData[filter, sort](NewDBProxy(model.db, nil)),
        WithFilter[filter, sort](req.Filter),
        WithSort[filter, sort](req.Sort),
        WithStart[filter, sort](req.Start),
        WithLimit[filter, sort](req.Limit),
    )
    if err != nil {
        return nil, 0, err
    }

    return result, total, nil
}
```

### Mock 测试

方便地为单元测试注入 Mock 策略：

```go
package main

import (
    "context"
    "testing"

    pb "demo/api/user/v1"
    "demo/internal/model"
    "go.uber.org/mock/gomock"
)

func TestQueryList(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()
	
	// 定义类型别名，简化泛型类型的推断
    type filter = pb.ListUserFilter
    type sort = pb.QueryUserListSort
    
    list := NewList[model.User, filter, sort](&UserService{})
    ctx := context.Background()
    
    // 创建 Mock 策略
    mockStrategy := NewMockQueryListStrategy[model.User](ctrl)
    mockStrategy.EXPECT().
    QueryList(ctx, gomock.Any()).
    Return([]*model.User{{ID: 1, Name: "Alice"}}, int64(1), nil)
    
    list.SetStrategy(mockStrategy)
}
```

---

## 贡献

欢迎提交 Issue 和 PR！

---

## License

MIT License

---

## 联系

如有问题或建议，请提交 Issue 或联系作者。