#include "expression.hpp"
#include <iostream>
void require(bool b) { if(!b) throw std::runtime_error("assertion failed"); }
int main() {
  using namespace rules;
  Variables vars{{"price",100},{"stock",3}};
  for(const auto& [source,expected]:std::vector<std::pair<std::string,double>>{{"1+2*3",7},{"(1+2)*3",9},{"8/2/2",2},{"-2*-3",6},{"price*0.8",80},{"stock>0 && price<=100",1},{"0 && 1/0",0},{"1 || missing",1},{"!(2==2)",0},{"2!=3",1}})
    require(Parser(source).compile()->evaluate(vars)==expected);
  for(auto source:{"", "1+", "(1", "1 2", "@", "1e999", "1/0", "missing", "1e308*1e308"}) {
    bool failed=false; try { Parser(source).compile()->evaluate(vars); } catch(const std::runtime_error&) { failed=true; } require(failed);
  }
  for(auto source:{std::string(100,'!')+"1",std::string(5000,'1')}) {
    bool failed=false; try { Parser(source).compile(); } catch(const std::runtime_error&) { failed=true; } require(failed);
  }
  for(int i=-100;i<=100;++i) {
    Variables v{{"x",static_cast<double>(i)}};
    require(Parser("(x*x+3*x-2)/2").compile()->evaluate(v)==(i*i+3.0*i-2)/2);
  }
  std::cout << "222 expression checks passed\n";
}
