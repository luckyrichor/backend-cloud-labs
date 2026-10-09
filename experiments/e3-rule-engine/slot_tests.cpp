#include "expression.hpp"
#include <array>
#include <limits>
#include <iostream>

int main() {
 std::vector<std::string> schema={"stock","price"};
 std::vector<std::string> expressions={"stock+price","stock-price","stock*price","stock/(price+1)","stock<price","stock<=price","stock>price","stock>=price","stock==price","stock!=price","!stock","-stock","stock && price","stock || price","stock>0 && price*0.8<=100"};
 std::size_t checks=0;
 for(const auto& text:expressions){
  auto rule=rules::Parser(text).compile();auto bound=rule->bind_variables(schema);
  for(int i=0;i<100;++i){
   std::array<double,2> values={double(i%4),double(i%17)};
   rules::Variables map={{"stock",values[0]},{"price",values[1]}};
   if(rule->evaluate(map)!=bound.evaluate(std::span<const double>(values)))throw std::runtime_error("differential mismatch");
   ++checks;
  }
 }
 auto shorted=rules::Parser("stock && price").compile();auto shortbound=shorted->bind_variables(schema);
 std::array<double,1> zero={0};if(shortbound.evaluate(std::span<const double>(zero))!=0)throw std::runtime_error("short circuit lost");
 auto rejects=[](auto fn){try{fn();}catch(const std::runtime_error&){return;}throw std::runtime_error("missing rejection");};
 std::array<double,1> one={1};rejects([&]{shortbound.evaluate(std::span<const double>(one));});
 std::array<double,2> nan={1,std::numeric_limits<double>::quiet_NaN()};rejects([&]{shortbound.evaluate(std::span<const double>(nan));});
 rejects([&]{shorted->bind_variables(std::vector<std::string>{"stock"});});
 rejects([&]{shorted->bind_variables(std::vector<std::string>{"stock","stock"});});
 // Failed rebind preserves the old valid schema.
 std::array<double,2> good={1,2};if(shortbound.evaluate(std::span<const double>(good))!=1)throw std::runtime_error("binding corrupted");
 // A second binding has its own layout; modifying the parsed tree cannot affect either.
 auto layout=rules::Parser("stock-price").compile();
 const auto normal=layout->bind_variables(schema);
 const auto swapped=layout->bind_variables(std::vector<std::string>{"price","stock"});
 layout.reset();
 std::array<double,2> reverse={2,5};
 if(swapped.evaluate(reverse)!=3 || normal.evaluate(reverse)!=-3) throw std::runtime_error("independent schema mismatch");
 shorted->op=rules::Op::Number;shorted->number=99;
 if(shortbound.evaluate(good)!=1) throw std::runtime_error("bound tree aliases mutable parser");
 auto div=rules::Parser("stock/price").compile();auto divbound=div->bind_variables(schema);
 std::array<double,2> invalid={1,0};rejects([&]{divbound.evaluate(std::span<const double>(invalid));});
 std::cout<<checks<<" map/slot differential checks plus short circuit and errors passed\n";
}
