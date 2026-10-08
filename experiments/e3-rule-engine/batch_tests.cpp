#include "expression.hpp"
#include "string_baseline.hpp"
#include <iostream>
#include <limits>

std::size_t checks{};
void require(bool ok) { ++checks; if (!ok) throw std::runtime_error("assertion failed"); }
template<class F> std::string error(F&& f) {
  try { f(); } catch (const std::runtime_error& e) { return e.what(); }
  throw std::runtime_error("expected error");
}
int main() {
  const std::vector<std::string> sources = {
    "x+y*2", "(x-y)/2", "-x", "!x", "x<y", "x<=y", "x>y", "x>=y",
    "x==y", "x!=y", "x && y", "x || y", "x>0 && missing", "x<=0 || missing",
    "0 && 1/0", "1 || missing", "1e308*1e308", "x/0"};
  for (const auto& source : sources) {
    auto current=rules::Parser(source).compile();
    auto old=string_baseline::Parser(source).compile();
    for (int x=-8; x<=8; ++x) for (int y=-8; y<=8; ++y) {
      rules::Variables vars{{"x",double(x)},{"y",double(y)}};
      std::string current_error,old_error; double a{},b{};
      try { a=current->evaluate(vars); } catch(const std::runtime_error& e) { current_error=e.what(); }
      try { b=old->evaluate(vars); } catch(const std::runtime_error& e) { old_error=e.what(); }
      require(current_error==old_error);
      if (current_error.empty()) require(a==b);
    }
  }
  auto compiled=rules::Parser("stock>0 && price*0.8<=100").compile();
  std::vector<rules::Variables> docs;
  for (int i=0;i<10000;++i) docs.push_back({{"stock",double(i%4)},{"price",double(80+i%80)}});
  auto original=docs;
  auto values=compiled->evaluate_batch(docs);
  auto matches=compiled->filter_batch(docs);
  std::vector<std::size_t> expected;
  for(std::size_t i=0;i<docs.size();++i) {
    require(values[i]==compiled->evaluate(docs[i]));
    if(values[i]!=0) expected.push_back(i);
  }
  require(matches==expected); require(docs==original);
  require(compiled->evaluate_batch({}).empty()); require(compiled->filter_batch({}).empty());
  // Short circuit must suppress missing variables in every row, including batch.
  std::vector<rules::Variables> short_docs(3,{{"stock",0}});
  require(compiled->filter_batch(short_docs).empty());
  short_docs[1]["stock"]=1;
  require(error([&]{compiled->filter_batch(short_docs);})=="document 1: invalid variable");
  require(error([&]{compiled->evaluate_batch(short_docs);})=="document 1: invalid variable");
  for (double value : {std::numeric_limits<double>::infinity(), std::numeric_limits<double>::quiet_NaN()}) {
    std::vector<rules::Variables> bad{{{"x",value}}};
    require(error([&]{rules::Parser("x").compile()->evaluate_batch(bad);})=="document 0: invalid variable");
  }
  require(rules::Parser("-2").compile()->filter_batch(std::vector<rules::Variables>(2))==std::vector<std::size_t>({0,1}));
  for(const auto& source : {std::string("1+")+"1+"+"1", std::string(129,'!')+"1", std::string(4097,'1')}) {
    std::string a,b;
    try {rules::Parser(source).compile();} catch(const std::runtime_error& e) {a=e.what();}
    try {string_baseline::Parser(source).compile();} catch(const std::runtime_error& e) {b=e.what();}
    require(a==b);
  }
  std::string too_many="1"; for(int i=0;i<129;++i) too_many+="+1";
  require(error([&]{rules::Parser(too_many).compile();})=="node limit");
  std::cout << checks << " batch/differential checks passed\n";
}
