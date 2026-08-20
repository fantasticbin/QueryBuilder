package main

import (
	"context"
	"fmt"
	"log"

	"github.com/olivere/elastic/v7"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"gorm.io/gorm"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/examples/internal/demo"
)

func main() {
	db, err := demo.Open()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	spec := agg.Spec{
		Groups: []agg.Group{{Field: "region", Alias: "region"}},
		Metrics: []agg.Metric{
			{Func: agg.Count, Alias: "order_count"},
			{Func: agg.Count, Field: "customer_id", Alias: "buyer_count", Distinct: true},
			{Func: agg.Sum, Field: "amount", Alias: "amount_sum"},
		},
		Limit: 10,
	}

	fmt.Println("== GORM ==")
	gormQuery := agg.NewGormBuilder[demo.Order, demo.SalesSummary](
		builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)),
	)
	gormQuery.SetSpec(spec)
	gormQuery.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", "paid")
	})
	printExplain(gormQuery.Explain(ctx))
	printNotes(agg.AnalyzeSpec(builder.Gorm, spec))

	fmt.Println("== MongoDB (Explain only) ==")
	mongoQuery := agg.NewMongoBuilder[demo.SalesSummary](
		builder.NewDBProxyWithAdapters(builder.NewMongoAdapter(&mongo.Collection{})),
	)
	mongoQuery.SetSpec(spec)
	mongoQuery.SetFilter(bson.D{{Key: "status", Value: "paid"}})
	printExplain(mongoQuery.Explain(ctx))
	printNotes(agg.AnalyzeSpec(builder.MongoDB, spec))

	fmt.Println("== ElasticSearch (Explain only) ==")
	esQuery := agg.NewElasticSearchBuilder[demo.SalesSummary](
		builder.NewDBProxyWithAdapters(builder.NewElasticSearchAdapter(&elastic.Client{})),
		"orders",
	)
	esQuery.SetSpec(spec)
	esQuery.SetFilter(elastic.NewTermQuery("status", "paid"))
	printExplain(esQuery.Explain(ctx))
	printNotes(agg.AnalyzeSpec(builder.ElasticSearch, spec))
}

func printExplain(text string, err error) {
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(text)
}

func printNotes(plan agg.Plan) {
	if len(plan.Notes) == 0 {
		fmt.Println("Plan.Notes: (none)")
		return
	}
	fmt.Println("Plan.Notes:")
	for _, note := range plan.Notes {
		fmt.Println(" -", note)
	}
}
