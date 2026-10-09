#include "expression.hpp"
#include <iostream>
int main(){
 for(const auto& text:{"0x10","0x1p3","0Xff",".0x1","1e","1e+","1e-",".","1.2.3","1e309","1,2"}) {
  bool rejected=false;try{rules::Parser(text).compile();}catch(const std::runtime_error&){rejected=true;}
  if(!rejected)throw std::runtime_error(std::string("accepted ")+text);
 }
 for(const auto& [text,want]:std::vector<std::pair<std::string,double>>{{"16",16},{".5",.5},{"1.",1},{"1e3",1000},{"2.5e-1",.25},{"1E+2",100},{"-2e1",-20}}){
  if(rules::Parser(text).compile()->evaluate(rules::Variables{})!=want)throw std::runtime_error("decimal changed");
 }
 std::cout<<"decimal grammar boundaries passed\n";
}
